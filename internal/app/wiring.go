package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"relayhub/internal/adapter"
	"relayhub/internal/adapter/agentrouter"
	"relayhub/internal/adapter/gorouter"
	"relayhub/internal/adapter/justdowork"
	"relayhub/internal/adapter/kktoken"
	"relayhub/internal/adapter/seekai"
	"relayhub/internal/affinity"
	"relayhub/internal/api"
	"relayhub/internal/audit"
	"relayhub/internal/auth"
	"relayhub/internal/browser"
	"relayhub/internal/catalog"
	"relayhub/internal/checkin"
	"relayhub/internal/domain"
	"relayhub/internal/egress"
	"relayhub/internal/gateway"
	"relayhub/internal/guard"
	"relayhub/internal/health"
	"relayhub/internal/keybind"
	"relayhub/internal/lab"
	"relayhub/internal/logging"
	"relayhub/internal/notify"
	"relayhub/internal/proxy"
	"relayhub/internal/ratelimit"
	"relayhub/internal/repository"
	"relayhub/internal/router"
	"relayhub/internal/secrets"
	"relayhub/internal/storage"
	"relayhub/internal/usage"
	"relayhub/internal/verify"
)

type Runtime struct {
	DB          *storage.DB
	Repo        *repository.Store
	SecretStore *secrets.Store
	Catalog     *catalog.Service
	Health      *health.Registry
	Router      *router.Resolver
	Adapters    *adapter.Registry
	Scheduler   *checkin.Scheduler
	LocalKeys   *auth.LocalKeyService
	HTTPProxy   *proxy.HTTPServer
	SOCKSProxy  *proxy.SOCKS5Server
	APIServer   *api.Server
	APIListener net.Listener
	GatewaySrv  *http.Server
	GWListener  net.Listener
	AuditLogger *audit.Logger
	// MgmtHTTP is the management http.Server itself (kept so Shutdown can wait
	// for in-flight management requests instead of hard-cutting the listener).
	MgmtHTTP *http.Server
	Notifier *notify.Dispatcher
	// ManagementTokenSource says where the management token came from:
	// "config", "off", or the path of <data-dir>/management.token.
	ManagementTokenSource string
	// Verify holds passive and probe-based authenticity scores; the router
	// demotes channels it marks suspect.
	Verify *verify.Registry

	// Periodic authenticity probes (AUDIT §5 B1); probeEvery 0 disables.
	probeEvery  time.Duration
	probePass   func(context.Context)
	probeCancel context.CancelFunc

	// Cheap recovery probes for tripped channels (AUDIT §5 B5);
	// healthProbeEvery 0 disables the loop and the breaker gate.
	healthProbeEvery time.Duration
	healthProbePass  func(context.Context)

	stopCh  chan struct{}
	stopped chan struct{}
	mu      sync.Mutex
}

func WireRuntime(ctx context.Context, cfg Config) (*Runtime, error) {
	return wire(ctx, cfg)
}

func wire(ctx context.Context, cfg Config) (*Runtime, error) {
	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve data dir: %w", err)
	}

	if err := os.MkdirAll(absDataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}
	// MkdirAll only applies 0700 to directories it creates; a pre-existing
	// dir (Docker's /data, a hand-made dir, an older install) kept 0755 and
	// the database landed world-readable (AUDIT 2026-09-24 F19).
	restrictToOwner(absDataDir, 0o700)

	mgmtToken, mgmtTokenSource, err := resolveManagementToken(absDataDir, cfg.ManagementAuth, cfg.ManagementToken)
	if err != nil {
		return nil, err
	}

	// Database
	dbPath := filepath.Join(absDataDir, "relayhub.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}
	// SQLite creates files per umask (typically 0644); the DB holds proxy
	// URLs, custom headers and request metadata. WAL/SHM files inherit the
	// database file's mode once it is restricted.
	for _, f := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		restrictToOwner(f, 0o600)
	}
	// config.json may hold the management token, proxy credentials and
	// notification secrets (AUDIT 2026-09-24 F20).
	restrictToOwner(filepath.Join(absDataDir, "config.json"), 0o600)

	repo := repository.New(db.DB)
	healthReg := health.NewRegistry()
	// Operator notifications (Webhook / Bark / Telegram). Disabled unless at
	// least one sink is configured; events are rate-limited per channel.
	notifier := notify.FromSettings(notify.Settings{
		WebhookURL:       cfg.Notify.WebhookURL,
		BarkURL:          cfg.Notify.BarkURL,
		TelegramBotToken: cfg.Notify.TelegramBotToken,
		TelegramChatID:   cfg.Notify.TelegramChatID,
	}, log.Printf)
	if notifier.Enabled() {
		log.Printf("relayhub: notifications enabled via %v", notifier.Sinks())
	}
	// One egress policy for every outbound path (gateway, check-in adapters,
	// browser): Channel.ProxyURL > global default > environment.
	egressSel := egress.New(cfg.EgressProxyURL)
	if cfg.EgressProxyURL != "" {
		if _, err := egress.Normalize(cfg.EgressProxyURL); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("invalid egress_proxy_url: %w", err)
		}
		log.Printf("relayhub: default egress via %s", egress.Describe(cfg.EgressProxyURL))
	}
	// Status transitions (breaker trips, quota exhaustion, recoveries) are
	// persisted so that history survives a restart and pushed to the
	// operator; per-request outcomes are not (they live in request_records).
	healthReg.SetObserver(composeHealthObservers(
		healthPersister(repo, log.Printf),
		healthNotifier(notifier, repoChannelLookup(repo)),
	))
	catalogSvc := catalog.NewService(repo)
	_ = catalogSvc.Load(ctx)

	// Build Resolver
	rProviders, rChannels, rModels, rProviderModels, rGroups, rMembers, rRoutes := catalogSvc.Snapshot()
	pmList := make([]domain.ProviderModel, 0, len(rProviderModels))
	for _, pm := range rProviderModels {
		pmList = append(pmList, pm)
	}
	routeList := make([]domain.Route, 0, len(rRoutes))
	for _, r := range rRoutes {
		routeList = append(routeList, r)
	}

	sticky := affinity.New(5 * time.Minute)
	res := &router.Resolver{
		Models:         rModels,
		ProviderModels: pmList,
		Channels:       rChannels,
		Providers:      rProviders,
		Groups:         rGroups,
		Members:        rMembers,
		Routes:         routeList,
		Health:         healthReg,
		Sticky:         sticky,
		// The "random" strategy selects by r.Rand when set; a counter source
		// would rotate deterministically instead (AUDIT RH-07).
		Rand: router.NewCryptoSource(),
	}

	// Secret Store
	keyProvider, err := newKeyProvider(ctx, cfg.MasterKeyStore, filepath.Join(absDataDir, "master.key"), runtime.GOOS)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create key provider: %w", err)
	}
	secStore, err := secrets.NewProductionStore(keyProvider, db.DB)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create secret store: %w", err)
	}
	if n, err := bindLegacyChannelKeys(ctx, repo, secStore); err != nil {
		log.Printf("relayhub: warning: binding legacy channel keys to their origin: %v", err)
	} else if n > 0 {
		log.Printf("relayhub: bound %d legacy channel key(s) to their channel's current origin", n)
	}

	// Local Auth
	sqlAuthBackend, err := auth.NewSQLKeyBackend(db.DB)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create auth backend: %w", err)
	}
	localKeys := auth.NewLocalKeyServiceWithBackend(sqlAuthBackend)

	// Adapter Registry. Check-in adapters MUST resolve real credentials through
	// the secret store; passing a CredentialRef straight to an upstream as a
	// bearer token was a confirmed defect (AUDIT-REPORT P1-9).
	adpReg := adapter.NewRegistry()
	_ = adpReg.Register("generic", adapter.NewGenericAdapterWithSecrets(nil, secStore))
	_ = adpReg.Register("agentrouter", agentrouter.NewWithSecrets(nil, secStore))
	_ = adpReg.Register("gorouter", gorouter.NewWithSecrets(nil, secStore))
	_ = adpReg.Register("justdowork", justdowork.NewWithSecrets(nil, secStore))
	_ = adpReg.Register("kktoken", kktoken.NewWithSecrets(nil, secStore))
	_ = adpReg.Register("seekai", seekai.NewWithSecrets(nil, secStore))
	adpReg.SetClientProvider(egressSel)

	// Scheduler
	sched := checkin.NewScheduler(checkin.Config{
		Interval:     24 * time.Hour,
		RandomJitter: 10 * time.Minute,
	}, adpReg, repo)
	// Balance observations (check-in follow-ups and the hourly poll) become a
	// routing signal: quota-first reads them from the health registry.
	quotaLow := quotaLowNotifier(notifier, cfg.Notify.QuotaLowUSD)
	sched.SetQuotaObserver(func(ch domain.Channel, q domain.QuotaSnapshot) {
		healthReg.SetQuota(ch.ID, quotaUpdateFrom(q), q.UpdatedAt)
		quotaLow(ch, q)
	})
	sched.SetEventSink(notifier.Notify)
	// Seed the registry from the last persisted snapshots so routing does not
	// start blind after a restart (stale snapshots are ignored).
	seedQuotaFromChannels(healthReg, rChannels, time.Now())

	// Proxies
	httpPAddr := cfg.HTTPProxyAddr
	if httpPAddr == "" {
		httpPAddr = "127.0.0.1:8787"
	}
	httpTargetPolicy, err := targetPolicyFor(cfg.HTTPProxyTargetPolicy)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("http proxy: %w", err)
	}
	// One guard shared by both proxies: it holds every RelayHub listener
	// port (proxies, gateway, management) so neither proxy can relay to the
	// loopback management API or to itself (AUDIT 2026-09-24 F1/F3). The
	// configured addresses are protected up front; the actual bound ports are
	// added below once the listeners exist (they differ when ":0" is used).
	selfGuard := proxy.NewSelfGuard()
	for _, a := range []string{cfg.GatewayAddr, cfg.ManagementAddr} {
		selfGuard.ProtectAddr(a)
	}
	httpProxy, err := proxy.NewHTTP(proxy.HTTPConfig{
		Addr:         httpPAddr,
		TargetPolicy: httpTargetPolicy,
		Guard:        selfGuard,
		Username:     cfg.ProxyUsername,
		Password:     cfg.ProxyPassword,
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create http proxy: %w", err)
	}

	socksAddr := cfg.SOCKS5Addr
	if socksAddr == "" {
		socksAddr = "127.0.0.1:8788"
	}
	socksTargetPolicy, err := targetPolicyFor(cfg.SOCKS5TargetPolicy)
	if err != nil {
		_ = httpProxy.Shutdown(ctx)
		_ = db.Close()
		return nil, fmt.Errorf("socks5 proxy: %w", err)
	}
	socksProxy, err := proxy.NewSOCKS5(proxy.SOCKS5Config{
		Addr:         socksAddr,
		TargetPolicy: socksTargetPolicy,
		Guard:        selfGuard,
		Username:     cfg.ProxyUsername,
		Password:     cfg.ProxyPassword,
	})
	if err != nil {
		_ = httpProxy.Shutdown(ctx)
		_ = db.Close()
		return nil, fmt.Errorf("failed to create socks proxy: %w", err)
	}

	log.Printf("relayhub: http proxy on %s (target policy: %s)", httpPAddr, describeTargetPolicy(httpTargetPolicy))
	log.Printf("relayhub: socks5 proxy on %s (target policy: %s)", socksAddr, describeTargetPolicy(socksTargetPolicy))

	// Audit logger
	sqlSink, err := audit.NewSQLSink(db.DB)
	if err != nil {
		_ = socksProxy.Shutdown(ctx)
		_ = httpProxy.Shutdown(ctx)
		_ = db.Close()
		return nil, fmt.Errorf("failed to create audit sink: %w", err)
	}
	auditLog := &audit.Logger{Sink: sqlSink}

	// Management API
	apiAddr := cfg.ManagementAddr
	if apiAddr == "" {
		apiAddr = "127.0.0.1:8790"
	}
	apiL, err := net.Listen("tcp", apiAddr)
	if err != nil {
		_ = socksProxy.Shutdown(ctx)
		_ = httpProxy.Shutdown(ctx)
		_ = db.Close()
		return nil, fmt.Errorf("failed to listen on api addr %s: %w", apiAddr, err)
	}
	selfGuard.ProtectAddr(apiL.Addr().String())

	refreshResolver := func(ctx context.Context) error {
		if err := catalogSvc.Load(ctx); err != nil {
			return err
		}
		p, c, m, pm, g, mem, r := catalogSvc.Snapshot()
		pms := make([]domain.ProviderModel, 0, len(pm))
		for _, v := range pm {
			pms = append(pms, v)
		}
		routes := make([]domain.Route, 0, len(r))
		for _, v := range r {
			routes = append(routes, v)
		}
		res.UpdateSnapshot(p, c, m, pms, g, mem, routes)
		return nil
	}

	// Gateway Server
	gwAddr := cfg.GatewayAddr
	if gwAddr == "" {
		gwAddr = "127.0.0.1:8789"
	}
	gwL, err := net.Listen("tcp", gwAddr)
	if err != nil {
		_ = apiL.Close()
		_ = socksProxy.Shutdown(ctx)
		_ = httpProxy.Shutdown(ctx)
		_ = db.Close()
		return nil, fmt.Errorf("failed to listen on gateway addr %s: %w", gwAddr, err)
	}
	selfGuard.ProtectAddr(gwL.Addr().String())

	browserRt := browser.NewRuntime()
	sched.SetBrowserExecutor(browser.NewCDPExecutor(browserRt, absDataDir, browser.Detect))
	sched.SetProxyResolver(egressSel.ProxyFor)

	// The address handed to CLI sync / settings must be dialable by a local
	// client: the *bound* port (a ":0" listener picks one) with any wildcard
	// host rewritten to loopback (a container binds 0.0.0.0, AUDIT RH-04).
	advertisedGW := advertisedAddr(gwL.Addr().String())
	for _, l := range []struct{ name, addr string }{{"http proxy", httpPAddr}, {"socks5 proxy", socksAddr}, {"gateway", gwAddr}} {
		if !isLoopbackListen(l.addr) {
			log.Printf("relayhub: WARNING %s listens on %s, which is reachable beyond this machine; only do this behind a firewall or a container port mapping bound to 127.0.0.1", l.name, l.addr)
		}
	}

	apiServer, err := api.NewConfiguredServer(api.Config{
		Management:     apiAddr,
		LocalOnly:      true,
		Token:          mgmtToken,
		DataDir:        absDataDir,
		Repo:           repo,
		SecretStore:    secStore,
		AuditLogger:    auditLog,
		LocalKeys:      localKeys,
		OnConfigChange: refreshResolver,
		Logger:         logging.New(os.Stdout),
		BackupDir:      filepath.Join(absDataDir, "backups"),
		GatewayAddr:    advertisedGW,
		BrowserRuntime: browserRt,
		Scheduler:      sched,
		Health:         healthReg,
		DefaultEgress:  cfg.EgressProxyURL,
		Egress:         egressSel,
		Notifier:       notifier,
	})
	if err != nil {
		_ = browserRt.Close(ctx)
		_ = gwL.Close()
		_ = apiL.Close()
		_ = socksProxy.Shutdown(ctx)
		_ = httpProxy.Shutdown(ctx)
		_ = db.Close()
		return nil, err
	}

	// Wire auto-sync: the scheduler triggers model refresh for channels with
	// auto_sync enabled, reusing the API server's fetch+filter+bind logic.
	sched.SetModelSync(apiServer.SyncChannelModels)

	verifyReg := verify.New()
	labRing := lab.NewRing(32)
	aimd := ratelimit.New()
	runaway := guard.New()
	apiServer.WithControlPlane(verifyReg, labRing, sticky, aimd)
	// Channels that fail an authenticity probe yield to trusted ones.
	res.Trust = verifyReg

	// keyRotation drives round-robin selection across a channel's enabled keys.
	var keyRotation atomic.Uint64
	upstream := gateway.HTTPUpstream{
		PickCredential: func(ctx context.Context, d router.Decision) (gateway.CredentialPick, error) {
			token, keyID, err := resolveChannelCredential(ctx, repo, secStore, &keyRotation, d.Channel, d.PreferredKeyID)
			return gateway.CredentialPick{Token: token, KeyID: keyID}, err
		},
		// Streaming responses must not be cut by a client timeout; the
		// request context bounds the call instead.
		ClientFor: egressSel.StreamingClientFor,
	}
	// Authenticity probes travel the exact path of client traffic: same
	// credential rotation, channel egress and identity headers.
	probes := &channelProber{
		resolver: res,
		prober:   gateway.Prober{Upstream: upstream},
		verify:   verifyReg,
		repo:     repo,
		health:   healthReg,
		notify:   notifier.Notify,
		logf:     log.Printf,
		spacing:  2 * time.Second,
	}
	apiServer.WithProber(probes.probe)
	if cfg.VerifyProbeInterval > 0 {
		log.Printf("relayhub: authenticity probes every %s", cfg.VerifyProbeInterval)
	}
	gwHandler := gateway.New(gateway.Config{
		Resolver: res,
		Upstream: upstream,
		Health:   healthReg,
		Auth:     localKeys,
		DisableKey: func(ctx context.Context, keyID string) {
			k, err := repo.GetChannelKey(ctx, keyID)
			if err != nil {
				return
			}
			k.Disabled = true
			if err := repo.UpdateChannelKey(ctx, k); err != nil {
				slog.Default().Warn("disable leaked/failed channel key", "key_id", keyID, "error", err)
			}
		},
		Recorder: usage.RepositoryRecorder{Repo: repo},
		Limiter:  aimd,
		Lab:      labRing,
		Guard:    runaway,
		Verify:   verifyReg,
	})

	gwServer := &http.Server{
		Handler:           gwHandler,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       0, // streaming completions may be long-lived; no hard read deadline
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	rt := &Runtime{
		DB:          db,
		Repo:        repo,
		SecretStore: secStore,
		Catalog:     catalogSvc,
		Health:      healthReg,
		Router:      res,
		Adapters:    adpReg,
		Scheduler:   sched,
		LocalKeys:   localKeys,
		HTTPProxy:   httpProxy,
		SOCKSProxy:  socksProxy,
		APIServer:   apiServer,
		APIListener: apiL,
		GatewaySrv:  gwServer,
		GWListener:  gwL,
		AuditLogger: auditLog,
		Notifier:    notifier,
		stopCh:      make(chan struct{}),
		// Source of the management credential, for the startup log.
		ManagementTokenSource: mgmtTokenSource,
		stopped:               make(chan struct{}),
		Verify:                verifyReg,
		probeEvery:            cfg.VerifyProbeInterval,
		probePass:             probes.pass,
		healthProbeEvery:      cfg.HealthProbeInterval,
		healthProbePass:       probes.healthPass,
	}

	return rt, nil
}

// resolveChannelCredential selects the credential the gateway injects upstream.
// It prefers per-channel API keys (round-robin over enabled keys) and falls back
// to the channel's legacy single CredentialRef when no channel keys exist.
func resolveChannelCredential(ctx context.Context, repo repository.ResourceRepository, secStore *secrets.Store, rot *atomic.Uint64, ch domain.Channel, preferredKeyID string) (string, string, error) {
	if keys, err := repo.ListChannelKeys(ctx, ch.ID); err == nil && len(keys) > 0 {
		enabled := make([]domain.ChannelKey, 0, len(keys))
		for _, k := range keys {
			// Keys are only released to the origin they were entered for
			// (AUDIT 2026-09-24 F5).
			if !k.Disabled && keybind.Allows(k.SecretRef, ch.ID, ch.BaseURL) {
				enabled = append(enabled, k)
			}
		}
		if len(enabled) > 0 {
			idx := 0
			if preferredKeyID != "" {
				for i, k := range enabled {
					if k.ID == preferredKeyID {
						idx = i
						break
					}
				}
			} else {
				idx = int(rot.Add(1)-1) % len(enabled)
			}
			if secretBytes, err := secStore.Get(ctx, enabled[idx].SecretRef); err == nil {
				return string(secretBytes), enabled[idx].ID, nil
			}
		}
	}
	if ch.CredentialRef == "" || !keybind.Allows(ch.CredentialRef, ch.ID, ch.BaseURL) {
		return "", "", nil
	}
	secretBytes, err := secStore.Get(ctx, ch.CredentialRef)
	if err != nil {
		return "", "", nil
	}
	return string(secretBytes), "", nil
}

// describeTargetPolicy renders a proxy target policy for operator-facing logs.
// The policy is a struct of booleans, so it must never be formatted with %s.
// advertisedAddr converts a bound listener address into one a local client
// can dial: wildcard hosts (0.0.0.0, ::, empty) become 127.0.0.1.
func advertisedAddr(bound string) string {
	host, port, err := net.SplitHostPort(bound)
	if err != nil {
		return bound
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return bound
}

// isLoopbackListen reports whether a listen address can only be reached from
// this machine ("localhost", 127.0.0.0/8, ::1).
func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// bindLegacyChannelKeys re-seals channel keys created before origin binding
// under a ref bound to the channel's current base_url origin, so they are no
// longer released to whatever host base_url is changed to later (AUDIT
// 2026-09-24 F5). The upgrade moment is trusted: the stored base_url is the
// one the operator entered the key for.
func bindLegacyChannelKeys(ctx context.Context, repo *repository.Store, store *secrets.Store) (int, error) {
	channels, err := repo.ListChannels(ctx, "")
	if err != nil {
		return 0, err
	}
	bound := 0
	for _, ch := range channels {
		if keybind.Origin(ch.BaseURL) == "" {
			continue
		}
		keys, err := repo.ListChannelKeys(ctx, ch.ID)
		if err != nil {
			return bound, err
		}
		for _, k := range keys {
			if !keybind.Unbound(k.SecretRef) || !keybind.Allows(k.SecretRef, ch.ID, ch.BaseURL) {
				continue
			}
			value, err := store.Get(ctx, k.SecretRef)
			if err != nil {
				continue
			}
			newRef := keybind.Bind(k.SecretRef, ch.BaseURL)
			if err := store.Put(ctx, newRef, value); err != nil {
				return bound, err
			}
			if err := repo.RebindChannelKeySecret(ctx, k.ID, newRef); err != nil {
				_ = store.Delete(ctx, newRef)
				return bound, err
			}
			_ = store.Delete(ctx, k.SecretRef)
			bound++
		}
	}
	return bound, nil
}

// restrictToOwner drops group/other permission bits from an existing path.
// Failures (e.g. a directory owned by another user) are logged, not fatal.
func restrictToOwner(path string, mode os.FileMode) {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 == 0 {
		return
	}
	if err := os.Chmod(path, mode); err != nil {
		log.Printf("relayhub: warning: %s is accessible to other users (mode %v) and could not be restricted: %v", path, info.Mode().Perm(), err)
	}
}

// targetPolicyFor maps a configured policy name to a proxy target policy.
// Unknown names fail startup instead of silently falling back to "open".
// RelayHub's own listener ports and cloud metadata addresses are refused by
// every policy (proxy.SelfGuard / metadata deny list).
func targetPolicyFor(name string) (proxy.TargetPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "open":
		return proxy.OpenPolicy(), nil
	case "local_only":
		return proxy.LocalOnlyPolicy(), nil
	case "public_private":
		return proxy.TargetPolicy{AllowPrivate: true, AllowPublic: true}, nil
	case "public_only":
		return proxy.TargetPolicy{AllowPublic: true}, nil
	}
	return proxy.TargetPolicy{}, fmt.Errorf("unknown proxy target policy %q (use open, public_private, public_only or local_only)", name)
}

func describeTargetPolicy(p proxy.TargetPolicy) string {
	switch {
	case p.AllowPublic && p.AllowPrivate && p.AllowLocal:
		return "open (local+private+public)"
	case !p.AllowPublic && !p.AllowPrivate && p.AllowLocal:
		return "local_only"
	case p.AllowPublic && p.AllowPrivate && !p.AllowLocal:
		return "public_private"
	case p.AllowPublic && !p.AllowPrivate && !p.AllowLocal:
		return "public_only"
	default:
		return fmt.Sprintf("custom (local=%t private=%t public=%t)", p.AllowLocal, p.AllowPrivate, p.AllowPublic)
	}
}

func (r *Runtime) Start(ctx context.Context) error {
	go func() { _ = r.HTTPProxy.Start(ctx) }()
	go func() { _ = r.SOCKSProxy.Start(ctx) }()
	go func() { _ = r.Scheduler.Start(ctx) }()
	if r.probeEvery > 0 && r.probePass != nil {
		probeCtx, cancel := context.WithCancel(ctx)
		r.probeCancel = cancel
		go runProbeLoop(probeCtx, r.probeEvery, probeInitialDelay, r.probePass)
	}

	apiSrv := &http.Server{
		Handler:           r.APIServer.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	} // read/write timeouts bound slowloris-style abuse (AUDIT RH-28).
	r.MgmtHTTP = apiSrv
	// Serve errors used to be discarded: a post-listen failure (or unexpected
	// exit) left the runtime "running" with no observable signal (RH-22).
	go func() {
		if err := apiSrv.Serve(r.APIListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("relayhub: management server exited unexpectedly: %v", err)
		}
	}()
	go func() {
		if err := r.GatewaySrv.Serve(r.GWListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("relayhub: gateway server exited unexpectedly: %v", err)
		}
	}()

	return nil
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	if r.probeCancel != nil {
		r.probeCancel()
	}
	_ = r.Scheduler.Stop(ctx)
	if r.MgmtHTTP != nil {
		_ = r.MgmtHTTP.Shutdown(ctx)
	}
	if r.APIServer != nil && r.APIServer.BrowserRuntime != nil {
		_ = r.APIServer.BrowserRuntime.Close(ctx)
	}
	_ = r.GatewaySrv.Shutdown(ctx)
	_ = r.APIListener.Close()
	_ = r.HTTPProxy.Shutdown(ctx)
	_ = r.SOCKSProxy.Shutdown(ctx)
	_ = r.DB.Close()
	if r.Notifier != nil {
		r.Notifier.Flush(3 * time.Second)
	}
	return nil
}

// newKeyProvider selects where the secret-store key lives (AUDIT 2026-09-24
// F18). "keychain" keeps it out of the data directory, so backups and sync
// folders no longer carry the key next to the ciphertext.
func newKeyProvider(ctx context.Context, store, keyPath, goos string) (secrets.KeyProvider, error) {
	switch strings.ToLower(strings.TrimSpace(store)) {
	case "", "file":
		return secrets.NewFileKeyProvider(keyPath)
	case "keychain":
		if goos != "darwin" {
			return nil, fmt.Errorf("master_key_store %q is only supported on macOS", store)
		}
		return secrets.NewKeychainKeyProvider(ctx, secrets.KeychainOptions{FilePath: keyPath})
	default:
		return nil, fmt.Errorf("unknown master_key_store %q (want \"file\" or \"keychain\")", store)
	}
}

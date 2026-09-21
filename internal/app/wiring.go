package app

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	Notifier    *notify.Dispatcher

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
	}

	// Secret Store
	keyProvider, err := secrets.NewFileKeyProvider(filepath.Join(absDataDir, "master.key"))
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create key provider: %w", err)
	}
	secStore, err := secrets.NewProductionStore(keyProvider, db.DB)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create secret store: %w", err)
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
	_ = adpReg.Register("generic", adapter.NewGenericAdapter(nil))
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
	httpTargetPolicy := proxy.OpenPolicy()
	if cfg.HTTPProxyTargetPolicy == "local_only" {
		httpTargetPolicy = proxy.LocalOnlyPolicy()
	}
	httpProxy, err := proxy.NewHTTP(proxy.HTTPConfig{
		Addr:         httpPAddr,
		TargetPolicy: httpTargetPolicy,
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create http proxy: %w", err)
	}

	socksAddr := cfg.SOCKS5Addr
	if socksAddr == "" {
		socksAddr = "127.0.0.1:8788"
	}
	socksTargetPolicy := proxy.OpenPolicy()
	if cfg.SOCKS5TargetPolicy == "local_only" {
		socksTargetPolicy = proxy.LocalOnlyPolicy()
	}
	socksProxy, err := proxy.NewSOCKS5(proxy.SOCKS5Config{
		Addr:         socksAddr,
		TargetPolicy: socksTargetPolicy,
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

	browserRt := browser.NewRuntime()
	sched.SetBrowserExecutor(browser.NewCDPExecutor(browserRt, absDataDir, browser.Detect))
	sched.SetProxyResolver(egressSel.ProxyFor)

	apiServer, err := api.NewConfiguredServer(api.Config{
		Management:     apiAddr,
		LocalOnly:      true,
		Repo:           repo,
		SecretStore:    secStore,
		AuditLogger:    auditLog,
		LocalKeys:      localKeys,
		OnConfigChange: refreshResolver,
		Logger:         logging.New(os.Stdout),
		BackupDir:      filepath.Join(absDataDir, "backups"),
		GatewayAddr:    gwAddr,
		BrowserRuntime: browserRt,
		Scheduler:      sched,
		Health:         healthReg,
		DefaultEgress:  cfg.EgressProxyURL,
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

	// keyRotation drives round-robin selection across a channel's enabled keys.
	var keyRotation atomic.Uint64
	gwHandler := gateway.New(gateway.Config{
		Resolver: res,
		Upstream: gateway.HTTPUpstream{
			PickCredential: func(ctx context.Context, d router.Decision) (gateway.CredentialPick, error) {
				token, keyID, err := resolveChannelCredential(ctx, repo, secStore, &keyRotation, d.Channel, d.PreferredKeyID)
				return gateway.CredentialPick{Token: token, KeyID: keyID}, err
			},
			// Streaming responses must not be cut by a client timeout; the
			// request context bounds the call instead.
			ClientFor: egressSel.StreamingClientFor,
		},
		Health: healthReg,
		Auth:   localKeys,
		DisableKey: func(ctx context.Context, keyID string) {
			k, err := repo.GetChannelKey(ctx, keyID)
			if err != nil {
				return
			}
			k.Disabled = true
			_ = repo.UpdateChannelKey(ctx, k)
		},
		Recorder: usage.RepositoryRecorder{Repo: repo},
		Limiter:  aimd,
		Lab:      labRing,
		Guard:    runaway,
		Verify:   verifyReg,
	})

	gwServer := &http.Server{
		Handler: gwHandler,
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
		stopped:     make(chan struct{}),
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
			if !k.Disabled {
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
	if ch.CredentialRef == "" {
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
func describeTargetPolicy(p proxy.TargetPolicy) string {
	switch {
	case p.AllowPublic && p.AllowPrivate && p.AllowLocal:
		return "open (local+private+public)"
	case !p.AllowPublic && !p.AllowPrivate && p.AllowLocal:
		return "local_only"
	default:
		return fmt.Sprintf("custom (local=%t private=%t public=%t)", p.AllowLocal, p.AllowPrivate, p.AllowPublic)
	}
}

func (r *Runtime) Start(ctx context.Context) error {
	go func() { _ = r.HTTPProxy.Start(ctx) }()
	go func() { _ = r.SOCKSProxy.Start(ctx) }()
	go func() { _ = r.Scheduler.Start(ctx) }()

	apiSrv := &http.Server{Handler: r.APIServer.Handler()}
	go func() { _ = apiSrv.Serve(r.APIListener) }()

	go func() { _ = r.GatewaySrv.Serve(r.GWListener) }()

	return nil
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	_ = r.Scheduler.Stop(ctx)
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

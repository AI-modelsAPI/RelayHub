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
	"relayhub/internal/gateway"
	"relayhub/internal/guard"
	"relayhub/internal/health"
	"relayhub/internal/lab"
	"relayhub/internal/logging"
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
	_ = adpReg.Register("generic", adapter.NewGenericAdapterWithSecrets(nil, secStore))
	_ = adpReg.Register("agentrouter", agentrouter.NewWithSecrets(nil, secStore))
	_ = adpReg.Register("gorouter", gorouter.NewWithSecrets(nil, secStore))
	_ = adpReg.Register("justdowork", justdowork.NewWithSecrets(nil, secStore))
	_ = adpReg.Register("kktoken", kktoken.NewWithSecrets(nil, secStore))
	_ = adpReg.Register("seekai", seekai.NewWithSecrets(nil, secStore))

	// Scheduler
	sched := checkin.NewScheduler(checkin.Config{
		Interval:     24 * time.Hour,
		RandomJitter: 10 * time.Minute,
	}, adpReg, repo)

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
		},
		Health: healthReg,
		Auth:   localKeys,
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
	return nil
}

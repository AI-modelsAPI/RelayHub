package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"relayhub/internal/buildinfo"
)

type Config struct {
	DataDir               string
	HTTPProxyAddr         string
	SOCKS5Addr            string
	GatewayAddr           string
	ManagementAddr        string
	HTTPProxyTargetPolicy string
	SOCKS5TargetPolicy    string
	// ProxyUsername / ProxyPassword enable authentication on both local
	// proxies when both are set (AUDIT 2026-09-24 F2).
	ProxyUsername string
	ProxyPassword string
	// MasterKeyStore is "file" (default) or "keychain" (macOS only).
	MasterKeyStore string
	WireFullStack  bool
	// EgressProxyURL is the global default exit for outbound traffic; a
	// channel's proxy_url overrides it. See internal/egress.
	EgressProxyURL string
	// ManagementToken is an explicit management API bearer token (AUDIT
	// 2026-09-24 F4).
	ManagementToken string
	// ManagementAuth: "token" requires a token on the management API,
	// generating <data-dir>/management.token when ManagementToken is empty;
	// "off" disables authentication; "" (embedders and tests) uses
	// ManagementToken as given, so an empty token means no authentication.
	// The relayhub binary passes config's default, "token".
	ManagementAuth string
	// Notify configures outbound notification sinks (see internal/notify).
	Notify NotifyConfig
	// VerifyProbeInterval is how often authenticity probes run (AUDIT §5
	// B1); 0 disables the periodic pass. The relayhub binary passes
	// config's verify_probe_interval (default 12h).
	VerifyProbeInterval time.Duration
}

// NotifyConfig mirrors config.NotifyConfig without importing the config
// package into app (main maps one onto the other).
type NotifyConfig struct {
	WebhookURL       string
	BarkURL          string
	TelegramBotToken string
	TelegramChatID   string
	QuotaLowUSD      float64
}

type App struct {
	cfg       Config
	info      buildinfo.Info
	runtime   *Runtime
	mu        sync.Mutex
	started   bool
	listeners int
}

func New(cfg Config) (*App, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("app: DataDir is required")
	}
	return &App{
		cfg:  cfg,
		info: buildinfo.Get(),
	}, nil
}

func (a *App) Info() buildinfo.Info {
	return a.info
}

func (a *App) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.started
}

func (a *App) Listeners() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.listeners
}

func (a *App) Runtime() *Runtime {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runtime
}

func (a *App) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return errors.New("app: already started")
	}

	if a.cfg.WireFullStack {
		rt, err := WireRuntime(ctx, a.cfg)
		if err != nil {
			return err
		}
		if err := rt.Start(ctx); err != nil {
			_ = rt.Shutdown(ctx)
			return err
		}
		a.runtime = rt
		a.listeners = 4 // HTTP Proxy, SOCKS5, API, Gateway
	}

	a.started = true
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started {
		return nil
	}

	var err error
	if a.runtime != nil {
		err = a.runtime.Shutdown(ctx)
		a.runtime = nil
	}

	a.started = false
	a.listeners = 0
	return err
}

package app

import (
	"context"
	"errors"
	"sync"

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
	WireFullStack         bool
	// EgressProxyURL is the global default exit for outbound traffic; a
	// channel's proxy_url overrides it. See internal/egress.
	EgressProxyURL string
	// Notify configures outbound notification sinks (see internal/notify).
	Notify NotifyConfig
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

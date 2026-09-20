package app

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestFullStackWiringLifecycle(t *testing.T) {
	dataDir := t.TempDir()

	cfg := Config{
		DataDir:        dataDir,
		HTTPProxyAddr:  "127.0.0.1:0",
		SOCKS5Addr:     "127.0.0.1:0",
		GatewayAddr:    "127.0.0.1:0",
		ManagementAddr: "127.0.0.1:0",
		WireFullStack:  true,
	}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !app.Running() {
		t.Fatal("expected app to be running")
	}
	if app.Listeners() != 4 {
		t.Fatalf("expected 4 listeners, got %d", app.Listeners())
	}

	rt := app.Runtime()
	if rt == nil {
		t.Fatal("expected runtime to be non-nil")
	}

	// Probe management API
	apiAddr := rt.APIListener.Addr().String()
	resp, err := http.Get("http://" + apiAddr + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("management healthz failed: %v, resp=%+v", err, resp)
	}
	_ = resp.Body.Close()

	// Shutdown
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()

	if err := app.Shutdown(shutCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	if app.Running() {
		t.Fatal("expected app to be stopped")
	}
	if app.Listeners() != 0 {
		t.Fatalf("expected 0 listeners after shutdown, got %d", app.Listeners())
	}
}

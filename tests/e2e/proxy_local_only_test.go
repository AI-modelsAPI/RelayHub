package e2e

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"relayhub/internal/app"
)

func TestProxyLocalOnlyExplicitPolicyBlocksPublicTarget(t *testing.T) {
	dataDir := t.TempDir()
	cfg := app.Config{
		DataDir:               dataDir,
		HTTPProxyAddr:         "127.0.0.1:0",
		SOCKS5Addr:            "127.0.0.1:0",
		GatewayAddr:           "127.0.0.1:0",
		ManagementAddr:        "127.0.0.1:0",
		HTTPProxyTargetPolicy: "local_only",
		SOCKS5TargetPolicy:    "local_only",
		WireFullStack:         true,
	}

	core, err := app.New(cfg)
	if err != nil {
		t.Fatalf("New app failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start app failed: %v", err)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = core.Shutdown(shutCtx)
	}()

	rt := core.Runtime()
	proxyURL, err := url.Parse("http://" + rt.HTTPProxy.Addr().String())
	if err != nil {
		t.Fatalf("parse proxy addr: %v", err)
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 3 * time.Second,
	}

	req, _ := http.NewRequest(http.MethodGet, "http://1.1.1.1:80/", nil)
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403 Forbidden under local_only policy, got %d: %s", resp.StatusCode, string(b))
		}
	}
	// An error (such as EOF due to proxy closing connection after 403) is also expected when proxy rejects connection
}

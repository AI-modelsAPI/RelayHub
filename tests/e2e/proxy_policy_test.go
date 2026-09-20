package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"relayhub/internal/app"
)

func TestProxyDefaultAllowsPublicTarget(t *testing.T) {
	// A mock server simulating a public upstream service (bound to loopback in test, but we test target policy behavior)
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxied-ok"))
	}))
	defer targetServer.Close()

	dataDir := t.TempDir()
	cfg := app.Config{
		DataDir:        dataDir,
		HTTPProxyAddr:  "127.0.0.1:0",
		SOCKS5Addr:     "127.0.0.1:0",
		GatewayAddr:    "127.0.0.1:0",
		ManagementAddr: "127.0.0.1:0",
		WireFullStack:  true,
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
		Timeout: 5 * time.Second,
	}

	// 1.1.1.1 or 8.8.8.8 represents a public target IP.
	// When default policy is LocalOnly, requesting an external IP returns 403 / target blocked by policy / EOF.
	// With the fix, public targets are permitted by default policy.
	// We can test by sending a request to 1.1.1.1:80 via proxy and asserting proxy doesn't block it with 403 Forbidden.
	req, _ := http.NewRequest(http.MethodGet, "http://1.1.1.1:80/", nil)
	resp, err := client.Do(req)
	if err != nil {
		// If proxy rejected connection with 403, http client sees EOF or 403
		t.Fatalf("request to public target via HTTP proxy failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("HTTP proxy blocked public target with 403: %s", string(b))
	}
	fmt.Printf("resp status from public IP via proxy: %d\n", resp.StatusCode)
}

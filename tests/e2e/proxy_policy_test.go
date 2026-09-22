package e2e

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"relayhub/internal/app"
)

// nonLoopbackTarget serves a tiny HTTP fixture on a LAN (non-loopback, non
// link-local) interface of this host. That gives the "open" proxy policy a
// real non-local address to admit without depending on the public internet,
// which made the previous version of this test fail on any machine whose
// network drops 1.1.1.1:443 (the 301 → https redirect turned the request into
// a CONNECT that timed out). Returns "" when no such interface exists.
func nonLoopbackTarget(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			ln, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
			if err != nil {
				continue
			}
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("proxied-ok"))
			}))
			srv.Listener = ln
			srv.Start()
			t.Cleanup(srv.Close)
			return srv.URL
		}
	}
	return ""
}

// TestProxyDefaultAllowsPublicTarget verifies the out-of-the-box proxy policy
// is "open": non-loopback targets are admitted (the explicit local_only policy
// is covered by TestProxyLocalOnlyExplicitPolicyBlocksPublicTarget).
func TestProxyDefaultAllowsPublicTarget(t *testing.T) {
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
		// Never follow redirects: a 301 to https:// would become a CONNECT to
		// port 443, which is exactly the network dependency this test avoids.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	if target := nonLoopbackTarget(t); target != "" {
		resp, err := client.Get(target)
		if err != nil {
			t.Fatalf("request to LAN target %s via HTTP proxy failed: %v", target, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || string(body) != "proxied-ok" {
			t.Fatalf("expected 200 proxied-ok from %s via proxy, got %d %q", target, resp.StatusCode, string(body))
		}
		return
	}

	// No usable LAN interface on this host: fall back to a public address. The
	// only assertion is that the proxy does not *deny* it. 403 is a policy
	// verdict; 200/301 (reachable) or 502 (network offline) both prove the open
	// policy let the dial proceed.
	req, _ := http.NewRequest(http.MethodGet, "http://1.1.1.1:80/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("no non-loopback interface and public fallback target unreachable through proxy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("HTTP proxy blocked public target with 403 under default policy: %s", string(b))
	}
	t.Logf("public fallback target via proxy answered %d (open policy admitted the dial)", resp.StatusCode)
}

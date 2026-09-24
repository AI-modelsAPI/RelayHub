package app

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// AUDIT 2026-09-24 F3: with the full stack wired, neither local proxy may be
// used to reach the loopback management API or the gateway.
func TestFullStackProxiesCannotReachManagementOrGateway(t *testing.T) {
	a, err := New(Config{
		DataDir:        t.TempDir(),
		HTTPProxyAddr:  "127.0.0.1:0",
		SOCKS5Addr:     "127.0.0.1:0",
		GatewayAddr:    "127.0.0.1:0",
		ManagementAddr: "127.0.0.1:0",
		WireFullStack:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		_ = a.Shutdown(sctx)
	}()
	rt := a.Runtime()
	mgmt := "http://" + rt.APIListener.Addr().String() + "/api/v1/settings"
	gw := "http://" + rt.GWListener.Addr().String() + "/v1/models"

	// Sanity: the management API itself answers direct requests.
	if resp, err := http.Get(mgmt); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("direct management request failed: %v %+v", err, resp)
	} else {
		_ = resp.Body.Close()
	}

	proxies := map[string]string{
		"http":   "http://" + rt.HTTPProxy.Addr().String(),
		"socks5": "socks5://" + rt.SOCKSProxy.Addr().String(),
	}
	for name, raw := range proxies {
		pu, _ := url.Parse(raw)
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
		for _, target := range []string{mgmt, gw} {
			resp, err := client.Get(target)
			if err == nil {
				body := resp.StatusCode
				_ = resp.Body.Close()
				if body != http.StatusForbidden {
					t.Fatalf("%s proxy reached %s (status %d); want refusal", name, target, body)
				}
				continue
			}
			// SOCKS5 refusals surface as a connect error.
			if !strings.Contains(err.Error(), "socks") && !strings.Contains(err.Error(), "proxy") {
				t.Fatalf("%s proxy to %s: unexpected error %v", name, target, err)
			}
		}
	}
}

func TestTargetPolicyForRejectsUnknownNames(t *testing.T) {
	for _, ok := range []string{"", "open", "local_only", "public_private", "public_only", " OPEN "} {
		if _, err := targetPolicyFor(ok); err != nil {
			t.Fatalf("targetPolicyFor(%q) error: %v", ok, err)
		}
	}
	if _, err := targetPolicyFor("locall_only"); err == nil {
		t.Fatal("typo in policy name must fail startup instead of silently becoming open")
	}
	p, _ := targetPolicyFor("public_only")
	if p.AllowLocal || p.AllowPrivate || !p.AllowPublic {
		t.Fatalf("public_only mapped to %+v", p)
	}
}

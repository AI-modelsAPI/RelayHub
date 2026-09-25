package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

func TestManagementTokenIsWiredIntoTheAPI(t *testing.T) {
	a, err := New(Config{DataDir: t.TempDir(), HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true, ManagementToken: "s3cret-token"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Shutdown(context.Background()) }()
	url := "http://" + a.Runtime().APIListener.Addr().String() + "/api/v1/keys"
	resp, err := http.Post(url, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("minting a key without the token: status %d, want 401", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer s3cret-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("token was not accepted")
	}
}

// AUDIT 2026-09-24 F2: proxy authentication existed in the proxies but could
// not be enabled from configuration.
func TestProxyCredentialsAreWired(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("upstream-ok"))
	}))
	defer upstream.Close()
	a, err := New(Config{DataDir: t.TempDir(), HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true, ProxyUsername: "alice", ProxyPassword: "pw-1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Shutdown(context.Background()) }()
	proxyAddr := a.Runtime().HTTPProxy.Addr().String()
	get := func(user *url.Userinfo) int {
		pu := &url.URL{Scheme: "http", Host: proxyAddr, User: user}
		c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
		resp, err := c.Get(upstream.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := get(nil); code != http.StatusProxyAuthRequired {
		t.Fatalf("no credentials: status %d, want 407", code)
	}
	if code := get(url.UserPassword("alice", "wrong")); code != http.StatusProxyAuthRequired {
		t.Fatalf("wrong credentials: status %d, want 407", code)
	}
	if code := get(url.UserPassword("alice", "pw-1")); code != http.StatusOK {
		t.Fatalf("valid credentials: status %d, want 200", code)
	}
}

func TestConfigFileIsRestricted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(cfgPath, 0o644)
	a, err := New(Config{DataDir: dir, HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Shutdown(context.Background()) }()
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config.json mode %v", info.Mode().Perm())
	}
}

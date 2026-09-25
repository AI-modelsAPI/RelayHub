package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// AUDIT 2026-09-24 F4: the management API is authenticated by default.
func TestResolveManagementToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManagementTokenFile)

	tok, src, err := resolveManagementToken(dir, "token", "")
	if err != nil || len(tok) != 64 || src != path {
		t.Fatalf("auto token: %q %q %v", tok, src, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", info, err)
	}
	again, _, err := resolveManagementToken(dir, " TOKEN ", "")
	if err != nil || again != tok {
		t.Fatalf("restart must reuse the token: %q %v", again, err)
	}
	if got, src, _ := resolveManagementToken(dir, "token", " explicit-token-value "); got != "explicit-token-value" || src != "config" {
		t.Fatalf("explicit token should win: %q %q", got, src)
	}
	if got, src, err := resolveManagementToken(dir, "off", ""); got != "" || src != "off" || err != nil {
		t.Fatalf("off: %q %q %v", got, src, err)
	}
	if _, _, err := resolveManagementToken(dir, "off", "explicit-token-value"); err == nil {
		t.Fatal("off plus an explicit token is contradictory")
	}
	if _, _, err := resolveManagementToken(dir, "sometimes", ""); err == nil {
		t.Fatal("unknown mode must fail")
	}
	if got, src, _ := resolveManagementToken(dir, "", ""); got != "" || src != "off" {
		t.Fatalf("embedding default: %q %q", got, src)
	}

	// A loosened or edited file is tightened and validated.
	_ = os.Chmod(path, 0o644)
	if _, _, err := resolveManagementToken(dir, "token", ""); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v", info.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveManagementToken(dir, "token", ""); err == nil {
		t.Fatal("a too-short token must be rejected")
	}
	_ = os.Remove(path)
	if err := os.Symlink("/etc/hostname", path); err == nil {
		if _, _, err := resolveManagementToken(dir, "token", ""); err == nil {
			t.Fatal("a symlinked token file must be rejected")
		}
	}
}

func TestFullStackManagementAuthOnByDefault(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Config{DataDir: dir, HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true, ManagementAuth: "token"})
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
	if rt.ManagementTokenSource != filepath.Join(dir, ManagementTokenFile) {
		t.Fatalf("token source %q", rt.ManagementTokenSource)
	}
	raw, err := os.ReadFile(rt.ManagementTokenSource)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))
	base := "http://" + rt.APIListener.Addr().String()
	get := func(path, tok string) int {
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := get("/api/v1/channels", ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous read: %d, want 401", code)
	}
	if code := get("/api/v1/channels", token); code != http.StatusOK {
		t.Fatalf("token read: %d, want 200", code)
	}
	if code := get("/api/v1/health", ""); code != http.StatusOK {
		t.Fatalf("health: %d", code)
	}
	if code := get("/", ""); code != http.StatusOK {
		t.Fatalf("console assets must stay reachable for pairing: %d", code)
	}
}

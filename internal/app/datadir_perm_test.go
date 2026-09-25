package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// AUDIT 2026-09-24 F19: a pre-existing 0755 data dir left relayhub.db 0644.
func TestExistingDataDirAndDatabaseAreRestricted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{DataDir: dir, HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	defer func() { _ = a.Shutdown(sctx) }()
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "relayhub.db"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got&0o077 != 0 {
			t.Fatalf("%s mode %v, want %v", path, got, want)
		}
	}
}

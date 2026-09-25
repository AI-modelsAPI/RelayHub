//go:build darwin

package secrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestKeychainProviderAgainstRealSecurity runs the provider against the real
// /usr/bin/security with a throwaway keychain file, so the login keychain is
// never touched (AUDIT 2026-09-24 F18). It covers the parts the fake runner
// cannot: security -i accepting the write on stdin, find-generic-password -w
// output, exit code 44 for a missing item, and failing closed on a locked
// keychain.
//
// Opt-in, since it creates and deletes a keychain:
// RELAYHUB_KEYCHAIN_TEST=1 go test ./internal/secrets -run RealSecurity
func TestKeychainProviderAgainstRealSecurity(t *testing.T) {
	if os.Getenv("RELAYHUB_KEYCHAIN_TEST") == "" {
		t.Skip("set RELAYHUB_KEYCHAIN_TEST=1 to exercise the real macOS keychain")
	}
	security := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("/usr/bin/security", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("security %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	kc := filepath.Join(t.TempDir(), "relayhub-test.keychain-db")
	security("create-keychain", "-p", "relayhub-test", kc)
	t.Cleanup(func() { _ = exec.Command("/usr/bin/security", "delete-keychain", kc).Run() })
	security("set-keychain-settings", kc) // no auto-lock during the test
	security("unlock-keychain", "-p", "relayhub-test", kc)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Migrate a legacy master.key: the keychain copy is read back, then
	// the file is removed.
	dataDir := t.TempDir()
	keyPath := filepath.Join(dataDir, "master.key")
	legacy := make([]byte, 32)
	if _, err := rand.Read(legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := NewKeychainKeyProvider(ctx, KeychainOptions{FilePath: keyPath, Keychain: kc})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Key(ctx); !bytes.Equal(got, legacy) {
		t.Fatal("migrated key differs from the legacy file")
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy key file still present after migration: %v", err)
	}
	stored := strings.TrimSpace(security("find-generic-password", "-s", keychainService, "-a", keychainAccount(keyPath), "-w", kc))
	if stored != hex.EncodeToString(legacy) {
		t.Fatal("keychain item does not hold the migrated key")
	}

	// 2. A restart reads the same key back from the keychain.
	again, err := NewKeychainKeyProvider(ctx, KeychainOptions{FilePath: keyPath, Keychain: kc})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.Key(ctx); !bytes.Equal(got, legacy) {
		t.Fatal("restart read a different key")
	}

	// 3. A stray, different master.key next to the keychain item is a
	// conflict, not a silent overwrite.
	other := make([]byte, 32)
	if _, err := rand.Read(other); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, other, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewKeychainKeyProvider(ctx, KeychainOptions{FilePath: keyPath, Keychain: kc}); err == nil {
		t.Fatal("conflicting key file accepted")
	}
	_ = os.Remove(keyPath)

	// 4. A fresh data directory (item missing, exit code 44) gets a new key.
	freshPath := filepath.Join(t.TempDir(), "master.key")
	fresh, err := NewKeychainKeyProvider(ctx, KeychainOptions{FilePath: freshPath, Keychain: kc})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := fresh.Key(ctx); len(got) != 32 || bytes.Equal(got, legacy) {
		t.Fatal("fresh install did not get its own key")
	}

	// 5. A locked keychain fails closed instead of minting a replacement for
	// the key it holds.
	security("lock-keychain", kc)
	if _, err := NewKeychainKeyProvider(ctx, KeychainOptions{FilePath: keyPath, Keychain: kc}); err == nil {
		t.Fatal("locked keychain: expected an error, got a key")
	}
	security("unlock-keychain", "-p", "relayhub-test", kc)
	stored = strings.TrimSpace(security("find-generic-password", "-s", keychainService, "-a", keychainAccount(keyPath), "-w", kc))
	if stored != hex.EncodeToString(legacy) {
		t.Fatal("the locked-keychain attempt replaced the stored key")
	}
}

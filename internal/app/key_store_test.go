package app

import (
	"context"
	"path/filepath"
	"testing"

	"relayhub/internal/secrets"
)

// AUDIT 2026-09-24 F18: the keychain store is opt-in and macOS-only; other
// values must fail loudly instead of silently falling back to the file.
func TestNewKeyProviderSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	for _, store := range []string{"", "file", " FILE "} {
		p, err := newKeyProvider(context.Background(), store, path, "linux")
		if err != nil {
			t.Fatalf("%q: %v", store, err)
		}
		if _, ok := p.(*secrets.FileKeyProvider); !ok {
			t.Fatalf("%q: got %T", store, p)
		}
	}
	if _, err := newKeyProvider(context.Background(), "keychain", path, "linux"); err == nil {
		t.Fatal("keychain on non-darwin must be a startup error")
	}
	if _, err := newKeyProvider(context.Background(), "vault", path, "darwin"); err == nil {
		t.Fatal("unknown store must be a startup error")
	}
}

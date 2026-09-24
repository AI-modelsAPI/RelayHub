package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/keybind"
	"relayhub/internal/repository"
	"relayhub/internal/secrets"
	"relayhub/internal/storage"
)

// AUDIT 2026-09-24 F5: keys created before origin binding are re-sealed under
// a ref bound to the channel's current origin at startup.
func TestLegacyChannelKeysAreBoundAtStartup(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := storage.Open(filepath.Join(dir, "relayhub.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	kp, err := secrets.NewFileKeyProvider(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secrets.NewProductionStore(kp, db.DB)
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db.DB)
	if err := repo.CreateProvider(ctx, domain.Provider{ID: "p1", Name: "p1", AdapterType: "generic", Protocol: "openai-chat", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateChannel(ctx, domain.Channel{ID: "c1", ProviderID: "p1", Name: "c1", BaseURL: "https://relay.example.com/v1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	legacyRef := "chkey:c1:chk-legacy"
	if err := store.Put(ctx, legacyRef, []byte("sk-legacy")); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateChannelKey(ctx, domain.ChannelKey{ID: "chk-legacy", ChannelID: "c1", SecretRef: legacyRef}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	a, err := New(Config{DataDir: dir, HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := a.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Shutdown(context.Background()) }()
	rt := a.Runtime()
	keys, err := rt.Repo.ListChannelKeys(ctx, "c1")
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	if o, bound := keybind.RefOrigin(keys[0].SecretRef); !bound || o != "https://relay.example.com:443" {
		t.Fatalf("legacy key not bound: %q", keys[0].SecretRef)
	}
	if v, err := rt.SecretStore.Get(ctx, keys[0].SecretRef); err != nil || string(v) != "sk-legacy" {
		t.Fatalf("re-sealed value lost: %q %v", v, err)
	}
	if _, err := rt.SecretStore.Get(ctx, legacyRef); err == nil {
		t.Fatal("old unbound secret should be deleted")
	}
}

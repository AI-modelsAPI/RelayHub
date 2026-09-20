package app

import (
	"context"
	"sync/atomic"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/secrets"
	"relayhub/internal/storage"
)

// TestResolveChannelCredentialRotatesAndSkipsDisabled proves the gateway
// credential resolver rotates across enabled per-channel keys and never hands
// out a disabled key, falling back to the legacy CredentialRef only when no
// channel keys exist.
func TestResolveChannelCredentialRotatesAndSkipsDisabled(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir() + "/rot.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db.DB)
	if err := repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "generic", Protocol: "openai-chat", BaseURLTemplate: "https://up.test", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ch := domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://up.test", Weight: 1, Enabled: true, CredentialRef: "legacy-ref"}
	if err := repo.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	kp, err := secrets.NewStaticKeyProvider([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	secStore := secrets.NewStoreWithBackend(kp, secrets.NewMemoryBackend())
	_ = secStore.Put(ctx, "legacy-ref", []byte("LEGACY"))

	var rot atomic.Uint64

	// No channel keys yet -> falls back to legacy CredentialRef.
	if got, _, _ := resolveChannelCredential(ctx, repo, secStore, &rot, ch, ""); got != "LEGACY" {
		t.Fatalf("expected legacy fallback, got %q", got)
	}

	// Add two enabled keys + one disabled.
	_ = secStore.Put(ctx, "ref-a", []byte("KEY_A"))
	_ = secStore.Put(ctx, "ref-b", []byte("KEY_B"))
	_ = secStore.Put(ctx, "ref-x", []byte("KEY_X_DISABLED"))
	_ = repo.CreateChannelKey(ctx, domain.ChannelKey{ID: "k1", ChannelID: "c", SecretRef: "ref-a"})
	_ = repo.CreateChannelKey(ctx, domain.ChannelKey{ID: "k2", ChannelID: "c", SecretRef: "ref-b"})
	_ = repo.CreateChannelKey(ctx, domain.ChannelKey{ID: "k3", ChannelID: "c", SecretRef: "ref-x", Disabled: true})

	// Rotate several times: must only ever see KEY_A / KEY_B, both appearing.
	rot.Store(0)
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		got, _, err := resolveChannelCredential(ctx, repo, secStore, &rot, ch, "")
		if err != nil {
			t.Fatal(err)
		}
		if got == "KEY_X_DISABLED" {
			t.Fatal("resolver handed out a disabled key")
		}
		if got == "LEGACY" {
			t.Fatal("resolver fell back to legacy while enabled keys exist")
		}
		seen[got]++
	}
	if seen["KEY_A"] == 0 || seen["KEY_B"] == 0 {
		t.Fatalf("rotation did not cover both enabled keys: %+v", seen)
	}

	// Disable all channel keys -> back to legacy fallback.
	k1, _ := repo.GetChannelKey(ctx, "k1")
	k1.Disabled = true
	_ = repo.UpdateChannelKey(ctx, k1)
	k2, _ := repo.GetChannelKey(ctx, "k2")
	k2.Disabled = true
	_ = repo.UpdateChannelKey(ctx, k2)
	if got, _, _ := resolveChannelCredential(ctx, repo, secStore, &rot, ch, ""); got != "LEGACY" {
		t.Fatalf("expected legacy fallback after disabling all keys, got %q", got)
	}
}

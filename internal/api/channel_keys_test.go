package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relayhub/internal/api"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/secrets"
	"relayhub/internal/storage"
)

// newKeyServer builds a repo+secret-store backed Server with one channel.
func newKeyServer(t *testing.T) (*api.Server, repository.ResourceRepository, *secrets.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(filepath.Join(t.TempDir(), "keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db.DB)
	if err := repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "generic", Protocol: "openai-chat", BaseURLTemplate: "https://up.test", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://up.test", Weight: 1, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	kp, err := secrets.NewStaticKeyProvider([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	secStore := secrets.NewStoreWithBackend(kp, secrets.NewMemoryBackend())
	srv, err := api.NewConfiguredServer(api.Config{Token: "test-token", Repo: repo, SecretStore: secStore, LocalOnly: true, StartedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return srv, repo, secStore
}

func keyDo(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestChannelKeyCreateNeverLeaksSecret(t *testing.T) {
	srv, _, secStore := newKeyServer(t)
	h := srv.Handler()
	w := keyDo(t, h, http.MethodPost, "/api/v1/channel-keys", `{"channel_id":"c","value":"sk-supersecret-123","label":"primary"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	// The raw secret must never appear in the response body.
	if strings.Contains(w.Body.String(), "sk-supersecret-123") {
		t.Fatalf("response leaked the secret value: %s", w.Body.String())
	}
	var out struct {
		Key struct {
			ID        string `json:"id"`
			Label     string `json:"label"`
			SecretRef string `json:"secret_ref"`
		} `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Key.ID == "" || out.Key.Label != "primary" {
		t.Fatalf("unexpected key metadata: %+v", out.Key)
	}
	if out.Key.SecretRef != "" {
		t.Fatalf("secret_ref must not be exposed to clients, got %q", out.Key.SecretRef)
	}
	// The value must actually be sealed in the secret store, retrievable server-side.
	all, err := secStore.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range all {
		if string(v) == "sk-supersecret-123" {
			found = true
		}
	}
	if !found {
		t.Fatal("secret value was not sealed into the secret store")
	}
}

func TestChannelKeyListReturnsMetadataOnly(t *testing.T) {
	srv, _, _ := newKeyServer(t)
	h := srv.Handler()
	keyDo(t, h, http.MethodPost, "/api/v1/channel-keys", `{"channel_id":"c","value":"sk-aaa","label":"k1"}`)
	keyDo(t, h, http.MethodPost, "/api/v1/channel-keys", `{"channel_id":"c","value":"sk-bbb","label":"k2"}`)

	w := keyDo(t, h, http.MethodGet, "/api/v1/channel-keys?channel_id=c", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-aaa") || strings.Contains(w.Body.String(), "sk-bbb") {
		t.Fatalf("list leaked secret values: %s", w.Body.String())
	}
	var out struct {
		Keys []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"keys"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(out.Keys))
	}
}

func TestChannelKeyDisableAndDelete(t *testing.T) {
	srv, repo, secStore := newKeyServer(t)
	h := srv.Handler()
	w := keyDo(t, h, http.MethodPost, "/api/v1/channel-keys", `{"channel_id":"c","value":"sk-x","label":"k"}`)
	var created struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id := created.Key.ID

	// Disable
	w = keyDo(t, h, http.MethodPatch, "/api/v1/channel-keys/"+id, `{"disabled":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", w.Code, w.Body.String())
	}
	k, _ := repo.GetChannelKey(context.Background(), id)
	if !k.Disabled {
		t.Fatal("key was not disabled")
	}
	secretRef := k.SecretRef

	// Delete: metadata gone AND sealed secret purged.
	w = keyDo(t, h, http.MethodDelete, "/api/v1/channel-keys/"+id, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := repo.GetChannelKey(context.Background(), id); err == nil {
		t.Fatal("key metadata still present after delete")
	}
	if _, err := secStore.Get(context.Background(), secretRef); err == nil {
		t.Fatal("sealed secret was not purged on key delete")
	}
}

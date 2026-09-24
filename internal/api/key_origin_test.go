package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"relayhub/internal/repository"
	"relayhub/internal/secrets"
	"relayhub/internal/storage"
)

// AUDIT 2026-09-24 F5: changing base_url and running a channel test sent the
// stored (write-only) upstream key to the new host.
func TestChannelKeyIsNotReleasedToANewOrigin(t *testing.T) {
	var leaked atomic.Value
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Authorization"); a != "" {
			leaked.Store(a)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"}]}`))
	}))
	defer attacker.Close()
	legit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-REAL-UPSTREAM-SECRET" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"}]}`))
	}))
	defer legit.Close()

	db, err := storage.Open(filepath.Join(t.TempDir(), "relayhub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	kp, _ := secrets.NewStaticKeyProvider(make([]byte, 32))
	store, err := secrets.NewProductionStore(kp, db.DB)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewConfiguredServer(Config{Repo: repository.NewSQLite(db.DB), SecretStore: store, LocalOnly: true, Management: "127.0.0.1:8790"})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	must := func(method, path, body string, want int) string {
		t.Helper()
		w := request(t, h, method, path, "", body)
		if w.Code != want && !(want == http.StatusCreated && w.Code == http.StatusOK) {
			t.Fatalf("%s %s: status %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.String()
	}
	must(http.MethodPost, "/api/v1/providers", `{"id":"p1","name":"p1","protocol":"openai-chat","adapter_type":"generic","enabled":true}`, http.StatusCreated)
	must(http.MethodPost, "/api/v1/channels", `{"id":"c1","provider_id":"p1","name":"c1","base_url":"`+legit.URL+`","enabled":true}`, http.StatusCreated)
	must(http.MethodPost, "/api/v1/channel-keys", `{"channel_id":"c1","value":"sk-REAL-UPSTREAM-SECRET","label":"k"}`, http.StatusCreated)
	if body := must(http.MethodPost, "/api/v1/channels/test", `{"channel_id":"c1"}`, http.StatusOK); !strings.Contains(body, `"success":true`) {
		t.Fatalf("legit origin test failed: %s", body)
	}

	must(http.MethodPut, "/api/v1/channels/c1", `{"id":"c1","provider_id":"p1","name":"c1","base_url":"`+attacker.URL+`","enabled":true}`, http.StatusOK)
	must(http.MethodPost, "/api/v1/channels/test", `{"channel_id":"c1"}`, http.StatusConflict)
	must(http.MethodPost, "/api/v1/fetch-models", `{"channel_id":"c1"}`, http.StatusOK)
	if v := leaked.Load(); v != nil {
		t.Fatalf("stored key leaked to the new origin: %v", v)
	}

	// A second channel cannot borrow c1's key through credential_ref either.
	must(http.MethodPost, "/api/v1/channels", `{"id":"c2","provider_id":"p1","name":"c2","base_url":"`+attacker.URL+`","credential_ref":"chkey:c1:x","enabled":true}`, http.StatusCreated)
	w := request(t, h, http.MethodPost, "/api/v1/channels/test", "", `{"channel_id":"c2"}`)
	if v := leaked.Load(); v != nil {
		t.Fatalf("credential_ref borrowed another channel's key: %v (status %d)", v, w.Code)
	}
}

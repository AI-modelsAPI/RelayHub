package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

func identityTestServer(t *testing.T) (http.Handler, *repository.SQLiteStore) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "relayhub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewSQLite(db.DB)
	s, err := NewConfiguredServer(Config{Repo: repo, LocalOnly: true, Management: "127.0.0.1:8790"})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	if w := request(t, h, http.MethodPost, "/api/v1/providers", "", `{"id":"p1","name":"p1","protocol":"openai-chat","adapter_type":"generic","enabled":true}`); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create provider: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, h, http.MethodPost, "/api/v1/channels", "", `{"id":"c1","provider_id":"p1","name":"c1","base_url":"https://relay.example.com","proxy_url":"socks5://alice:s3cret@127.0.0.1:1080","enabled":true}`); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create channel: %d %s", w.Code, w.Body.String())
	}
	return h, repo
}

func storedProxy(t *testing.T, repo *repository.SQLiteStore) string {
	t.Helper()
	ch, err := repo.GetChannel(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	return ch.ProxyURL
}

// AUDIT 2026-09-24 F12: PATCHing only the user agent cleared the channel's
// proxy, silently switching it to direct egress.
func TestIdentityPatchKeepsProxyWhenFieldAbsent(t *testing.T) {
	h, repo := identityTestServer(t)
	w := request(t, h, http.MethodPatch, "/api/v1/identity/c1", "", `{"user_agent":"Mozilla/5.0 test"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	if got := storedProxy(t, repo); got != "socks5://alice:s3cret@127.0.0.1:1080" {
		t.Fatalf("proxy_url changed to %q by a user_agent-only patch", got)
	}
	if strings.Contains(w.Body.String(), "s3cret") {
		t.Fatalf("PATCH response leaked proxy password: %s", w.Body.String())
	}
	ch, _ := repo.GetChannel(context.Background(), "c1")
	if ch.CustomHeaders["User-Agent"] != "Mozilla/5.0 test" {
		t.Fatalf("user agent not applied: %+v", ch.CustomHeaders)
	}
}

func TestIdentityPatchEchoOfMaskedBundleKeepsPassword(t *testing.T) {
	h, repo := identityTestServer(t)
	w := request(t, h, http.MethodGet, "/api/v1/identity", "", "")
	var list struct {
		Bundles []map[string]any `json:"bundles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Bundles) != 1 {
		t.Fatalf("list: %s %v", w.Body.String(), err)
	}
	b := list.Bundles[0]
	b["timezone"] = "Asia/Shanghai"
	body, _ := json.Marshal(b)
	if w := request(t, h, http.MethodPatch, "/api/v1/identity/c1", "", string(body)); w.Code != http.StatusOK {
		t.Fatalf("echo patch: %d %s", w.Code, w.Body.String())
	}
	if got := storedProxy(t, repo); got != "socks5://alice:s3cret@127.0.0.1:1080" {
		t.Fatalf("masked echo overwrote stored proxy: %q", got)
	}
}

func TestIdentityPatchValidatesAndClearsExplicitly(t *testing.T) {
	h, repo := identityTestServer(t)
	if w := request(t, h, http.MethodPatch, "/api/v1/identity/c1", "", `{"proxy_url":"ftp://x y"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid proxy accepted: %d %s", w.Code, w.Body.String())
	}
	if got := storedProxy(t, repo); got != "socks5://alice:s3cret@127.0.0.1:1080" {
		t.Fatalf("rejected patch still modified proxy: %q", got)
	}
	if w := request(t, h, http.MethodPatch, "/api/v1/identity/c1", "", `{"surprise":1}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d", w.Code)
	}
	if w := request(t, h, http.MethodPatch, "/api/v1/identity/c1", "", `{"channel_id":"other"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("mismatched channel_id accepted: %d", w.Code)
	}
	if w := request(t, h, http.MethodPatch, "/api/v1/identity/c1", "", `{"proxy_url":""}`); w.Code != http.StatusOK {
		t.Fatalf("explicit clear: %d %s", w.Code, w.Body.String())
	}
	if got := storedProxy(t, repo); got != "" {
		t.Fatalf("explicit empty proxy_url should clear, got %q", got)
	}
}

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

func modelListServer(t *testing.T, body *atomic.Value) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	t.Cleanup(up.Close)
	return up
}

func bindings(t *testing.T, repo *repository.SQLiteStore, channelID string) map[string]bool {
	t.Helper()
	pms, err := repo.ListProviderModels(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, pm := range pms {
		if pm.ChannelID == channelID {
			out[pm.ModelID] = pm.Enabled
		}
	}
	return out
}

// AUDIT 2026-09-24 F6: a relay that starts listing a model other channels
// already serve must not silently receive a share of that traffic through
// a background auto-sync.
func TestBackgroundSyncHoldsNewlyContestedModels(t *testing.T) {
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
	var trusted, relay, fresh atomic.Value
	trusted.Store(`{"data":[{"id":"gpt-4o"}]}`)
	relay.Store(`{"data":[{"id":"deepseek-r1"}]}`)
	fresh.Store(`{"data":[{"id":"gpt-4o"}]}`)
	upTrusted, upRelay, upFresh := modelListServer(t, &trusted), modelListServer(t, &relay), modelListServer(t, &fresh)

	if w := request(t, h, http.MethodPost, "/api/v1/providers", "", `{"id":"p1","name":"p1","protocol":"openai-chat","adapter_type":"generic","enabled":true}`); w.Code >= 300 {
		t.Fatalf("provider: %d %s", w.Code, w.Body.String())
	}
	for id, base := range map[string]string{"official": upTrusted.URL, "relay": upRelay.URL, "fresh": upFresh.URL} {
		body := `{"id":"` + id + `","provider_id":"p1","name":"` + id + `","base_url":"` + base + `","enabled":true}`
		if w := request(t, h, http.MethodPost, "/api/v1/channels", "", body); w.Code >= 300 {
			t.Fatalf("channel %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	for _, id := range []string{"official", "relay"} {
		if w := request(t, h, http.MethodPost, "/api/v1/channels/sync-models", "", `{"channel_id":"`+id+`"}`); w.Code != http.StatusOK {
			t.Fatalf("manual sync %s: %d %s", id, w.Code, w.Body.String())
		}
	}

	// The relay now also claims gpt-4o plus a model nobody else serves.
	relay.Store(`{"data":[{"id":"deepseek-r1"},{"id":"gpt-4o"},{"id":"brand-new"}]}`)
	if err := s.SyncChannelModels(context.Background(), "relay", ""); err != nil {
		t.Fatal(err)
	}
	got := bindings(t, repo, "relay")
	if enabled, ok := got["gpt-4o"]; !ok || enabled {
		t.Fatalf("contested model must be bound but held disabled: %v", got)
	}
	if !got["brand-new"] || !got["deepseek-r1"] {
		t.Fatalf("uncontested models must stay enabled: %v", got)
	}
	if !bindings(t, repo, "official")["gpt-4o"] {
		t.Fatal("the existing channel's binding must be untouched")
	}

	// Once the operator enables it, later background syncs keep it enabled.
	pms, _ := repo.ListProviderModels(context.Background(), "gpt-4o")
	for _, pm := range pms {
		if pm.ChannelID == "relay" {
			pm.Enabled = true
			if err := repo.UpdateProviderModel(context.Background(), pm); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.SyncChannelModels(context.Background(), "relay", ""); err != nil {
		t.Fatal(err)
	}
	if !bindings(t, repo, "relay")["gpt-4o"] {
		t.Fatal("operator approval must survive re-sync")
	}

	// A channel's first sync (onboarding) is not held, even in the background.
	if err := s.SyncChannelModels(context.Background(), "fresh", ""); err != nil {
		t.Fatal(err)
	}
	if !bindings(t, repo, "fresh")["gpt-4o"] {
		t.Fatal("first sync should enable models for load balancing")
	}
}

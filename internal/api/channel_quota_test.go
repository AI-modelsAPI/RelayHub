package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"relayhub/internal/api"
	"relayhub/internal/domain"
	"relayhub/internal/health"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

// A form save that omits quota_state must not erase the scheduler's last
// balance observation.
func TestChannelUpdatePreservesSystemOwnedFields(t *testing.T) {
	srv, repo := newCatalogServer(t)
	h := srv.Handler()
	ctx := context.Background()
	snap := domain.QuotaSnapshot{AvailableUSD: 2.5, Remaining: 1250000, UpdatedAt: time.Now().UTC(), Source: "balance"}
	ch, _ := repo.GetChannel(ctx, "c")
	ch.QuotaState = snap.Encode()
	ch.ErrorMessage = "last upstream error"
	_ = repo.UpdateChannel(ctx, ch)

	w := catalogDo(t, h, http.MethodPut, "/api/v1/channels/c", `{"provider_id":"p","name":"Renamed","base_url":"https://up.test","weight":1,"enabled":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ := repo.GetChannel(ctx, "c")
	if got.Name != "Renamed" {
		t.Fatalf("rename not applied: %+v", got)
	}
	if q, ok := domain.ParseQuotaSnapshot(got.QuotaState); !ok || q.AvailableUSD != 2.5 {
		t.Fatalf("quota_state was erased by the form save: %q", got.QuotaState)
	}
	if got.ErrorMessage != "last upstream error" {
		t.Fatalf("error_message was erased: %q", got.ErrorMessage)
	}
}

// channel stats expose the live routing view so the UI can explain "why is
// this channel not receiving traffic".
func TestChannelStatsExposesLiveHealthAndQuota(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db.DB)
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "generic", Protocol: "openai-chat", Enabled: true})
	snap := domain.QuotaSnapshot{AvailableUSD: 0, Remaining: 0, UpdatedAt: time.Now().UTC(), Source: "checkin"}
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://up.test", Enabled: true, QuotaState: snap.Encode()})
	reg := health.NewRegistry()
	now := time.Now()
	reg.RecordOutcome("c", true, 80*time.Millisecond, now)
	reg.SetQuota("c", health.QuotaUpdate{Known: true}, now)
	srv, err := api.NewConfiguredServer(api.Config{Token: "test-token", Repo: repo, LocalOnly: true, StartedAt: now, Health: reg})
	if err != nil {
		t.Fatal(err)
	}
	w := catalogDo(t, srv.Handler(), http.MethodGet, "/api/v1/channels/stats?channel_id=c", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Stats struct {
			Quota *domain.QuotaSnapshot `json:"quota"`
			Live  struct {
				Status    string  `json:"status"`
				Available bool    `json:"available"`
				Reason    string  `json:"reason"`
				LatencyMS int     `json:"latency_ms"`
				QuotaUSD  float64 `json:"quota_usd"`
			} `json:"live"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Stats.Quota == nil || out.Stats.Quota.Source != "checkin" {
		t.Fatalf("structured quota missing: %s", w.Body.String())
	}
	live := out.Stats.Live
	if live.Status != "quota_exhausted" || live.Available || live.Reason != "quota exhausted" || live.LatencyMS != 80 {
		t.Fatalf("live view wrong: %+v (body %s)", live, w.Body.String())
	}
}

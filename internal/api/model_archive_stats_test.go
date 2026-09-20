package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"relayhub/internal/domain"
)

func TestModelBatchArchiveHidesFromCatalog(t *testing.T) {
	srv, repo := newCatalogServer(t)
	h := srv.Handler()
	ctx := context.Background()
	_ = repo.CreateModel(ctx, domain.Model{ID: "m1", DisplayName: "M1", Enabled: true})
	_ = repo.CreateModel(ctx, domain.Model{ID: "m2", DisplayName: "M2", Enabled: true})

	// Archive m2.
	w := catalogDo(t, h, http.MethodPost, "/api/v1/models/batch", `{"action":"archive","ids":["m2"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("archive status=%d body=%s", w.Code, w.Body.String())
	}
	m2, _ := repo.GetModel(ctx, "m2")
	if !m2.Archived || m2.Enabled {
		t.Fatalf("m2 not archived/disabled: %+v", m2)
	}

	// Default catalog hides archived.
	w = catalogDo(t, h, http.MethodGet, "/api/v1/models/catalog", "")
	var out struct {
		Catalog []struct {
			ID string `json:"id"`
		} `json:"catalog"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Catalog) != 1 || out.Catalog[0].ID != "m1" {
		t.Fatalf("archived model leaked into default catalog: %+v", out.Catalog)
	}

	// include_archived=true shows it.
	w = catalogDo(t, h, http.MethodGet, "/api/v1/models/catalog?include_archived=true", "")
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Catalog) != 2 {
		t.Fatalf("expected 2 with include_archived, got %d", len(out.Catalog))
	}

	// Unarchive restores it to default catalog.
	w = catalogDo(t, h, http.MethodPost, "/api/v1/models/batch", `{"action":"unarchive","ids":["m2"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("unarchive status=%d body=%s", w.Code, w.Body.String())
	}
	m2, _ = repo.GetModel(ctx, "m2")
	if m2.Archived {
		t.Fatal("m2 still archived after unarchive")
	}
}

func TestModelCreatePersistsIconAndArchive(t *testing.T) {
	srv, repo := newCatalogServer(t)
	h := srv.Handler()
	w := catalogDo(t, h, http.MethodPost, "/api/v1/models", `{"id":"m1","display_name":"M1","icon_url":"https://x/i.png","enabled":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	m, _ := repo.GetModel(context.Background(), "m1")
	if m.IconURL != "https://x/i.png" {
		t.Fatalf("icon_url not persisted: %+v", m)
	}
}

func TestChannelStatsAggregates(t *testing.T) {
	srv, repo := newCatalogServer(t)
	h := srv.Handler()
	ctx := context.Background()
	// Health record + request records for channel "c".
	_ = repo.CreateHealthRecord(ctx, domain.HealthRecord{ID: "h1", ChannelID: "c", Status: "healthy", LatencyMS: 10})
	_ = repo.CreateRequestRecord(ctx, domain.RequestRecord{ID: "r1", RequestID: "q1", ChannelID: "c", StatusCode: 200, LatencyMS: 100})
	_ = repo.CreateRequestRecord(ctx, domain.RequestRecord{ID: "r2", RequestID: "q2", ChannelID: "c", StatusCode: 500, LatencyMS: 200})

	w := catalogDo(t, h, http.MethodGet, "/api/v1/channels/stats?channel_id=c", "")
	if w.Code != http.StatusOK {
		t.Fatalf("stats status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Stats struct {
			RecentRequests int `json:"recent_requests"`
			RecentSuccess  int `json:"recent_success"`
			RecentErrors   int `json:"recent_errors"`
			AvgLatencyMS   int `json:"avg_latency_ms"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if out.Stats.RecentRequests != 2 || out.Stats.RecentSuccess != 1 || out.Stats.RecentErrors != 1 {
		t.Fatalf("wrong request aggregation: %+v", out.Stats)
	}
	if out.Stats.AvgLatencyMS != 150 {
		t.Fatalf("expected avg latency 150, got %d", out.Stats.AvgLatencyMS)
	}
}

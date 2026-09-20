package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"relayhub/internal/adapter"
	"relayhub/internal/api"
	"relayhub/internal/checkin"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

func TestCheckinAPI_EndToEndWire_RED(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test_checkin_api_red.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	repo := repository.New(db.DB)
	ctx := context.Background()

	p := domain.Provider{ID: "p1", Name: "Test Provider", AdapterType: "generic", Enabled: true}
	_ = repo.CreateProvider(ctx, p)
	c := domain.Channel{ID: "c1", ProviderID: "p1", Name: "Test Channel", BaseURL: "https://example.com", Enabled: true, CheckinEnabled: true, CheckinMode: "manual"}
	_ = repo.CreateChannel(ctx, c)

	registry := adapter.NewRegistry()
	if err := registry.Register("generic", adapter.NewGenericAdapter(nil)); err != nil {
		t.Fatal(err)
	}
	scheduler := checkin.NewScheduler(checkin.Config{}, registry, repo)
	srv, err := api.NewConfiguredServer(api.Config{
		LocalOnly: true,
		Repo:      repo,
		Scheduler: scheduler,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. GET /api/v1/checkin should report supported: true, not false
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/checkin", nil)
	wGet := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Fatalf("expected GET 200, got %d", wGet.Code)
	}
	var getResp map[string]any
	_ = json.Unmarshal(wGet.Body.Bytes(), &getResp)
	if getResp["supported"] != true {
		t.Errorf("expected supported: true, got %v (body: %s)", getResp["supported"], wGet.Body.String())
	}

	// 2. POST /api/v1/checkin should NOT return 501 Unsupported
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/checkin", strings.NewReader(`{}`))
	wPost := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("POST expected 200, got %d: %s", wPost.Code, wPost.Body.String())
	}
	var postResp struct {
		Results []struct {
			Status    string `json:"status"`
			ManualURL string `json:"manual_url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(wPost.Body.Bytes(), &postResp); err != nil {
		t.Fatal(err)
	}
	if len(postResp.Results) != 1 || postResp.Results[0].Status != "need_manual" || postResp.Results[0].ManualURL != "https://example.com" {
		t.Fatalf("manual result missing: %s", wPost.Body.String())
	}
	records, err := repo.ListCheckinRecords(ctx, "c1")
	if err != nil || len(records) != 1 || records[0].Status != "need_manual" {
		t.Fatalf("manual result not persisted: %+v %v", records, err)
	}
}

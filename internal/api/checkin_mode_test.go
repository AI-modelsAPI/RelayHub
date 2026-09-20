package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relayhub/internal/api"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

func TestChannelAPI_CheckinMode(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(filepath.Join(t.TempDir(), "test_api_checkin_mode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	repo := repository.New(db.DB)
	p := domain.Provider{
		ID:              "p-api",
		Name:            "Provider API",
		AdapterType:     "generic",
		Protocol:        "openai-chat",
		BaseURLTemplate: "https://api.test",
		Capabilities:    "chat",
		Enabled:         true,
	}
	if err := repo.CreateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}

	srv, err := api.NewConfiguredServer(api.Config{
		Token:     "test-token",
		Repo:      repo,
		LocalOnly: true,
		StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	// 1. Post new channel with checkin_mode="manual"
	body := `{"id":"c-manual","provider_id":"p-api","name":"Channel Manual","base_url":"https://api.test","checkin_mode":"manual","enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/channels failed with %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"checkin_mode":"manual"`) {
		t.Fatalf("expected checkin_mode in response, got: %s", w.Body.String())
	}

	// 2. Reject invalid checkin_mode
	badBody := `{"id":"c-bad","provider_id":"p-api","name":"Channel Bad","base_url":"https://api.test","checkin_mode":"invalid_mode","enabled":true}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/channels", strings.NewReader(badBody))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for invalid checkin_mode, got %d: %s", w.Code, w.Body.String())
	}
}

package gorouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"relayhub/internal/adapter/testhelper"
	"relayhub/internal/domain"
)

func TestGoRouter_All(t *testing.T) {
	fixturePath := filepath.Join("..", "fixtures", "gorouter", "responses.json")
	fixtures, err := testhelper.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("failed to load fixtures: %v", err)
	}

	var selfResp []byte = fixtures["self_success_not_checked"]

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify CredentialRef is NEVER sent as Bearer token
		authHeader := r.Header.Get("Authorization")
		if authHeader == "Bearer cred-go-1" {
			t.Errorf("SECURITY VIOLATION: CredentialRef sent as Bearer token: %s", authHeader)
		}

		switch r.URL.Path {
		case "/api/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fixtures["status_success"])
		case "/api/user/self":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(selfResp)
		case "/api/user/checkin":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fixtures["checkin_status_empty"])
		case "/api/log/self":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fixtures["log_empty"])
		case "/api/data/self":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"data":[]}}`))
		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	secStore := testhelper.NewMockSecretStore()
	_ = secStore.Put(context.Background(), "cred-go-1", []byte(`{"token":"gorouter-secret-token"}`))

	adp := NewWithSecrets(srv.Client(), secStore)
	ch := domain.Channel{
		ID:            "ch-gorouter",
		BaseURL:       srv.URL,
		CredentialRef: "cred-go-1",
	}

	t.Run("CheckIn_NeverFalselyReportsSuccess", func(t *testing.T) {
		// When no checkin record in calendar/log and self.checked_in=false:
		// Must NOT report Success: true!
		selfResp = fixtures["self_success_not_checked"]

		res, _ := adp.CheckIn(context.Background(), ch)
		if res.Success {
			t.Fatalf("CRITICAL BUG: GoRouter CheckIn reported Success: true when no checkin record existed!")
		}
		if res.Already {
			t.Errorf("expected Already: false")
		}
	})

	t.Run("CheckIn_WithSelfCheckedInFallback", func(t *testing.T) {
		// For non-relogin sites like GoRouter, self.checked_in=true is accepted as fallback
		selfResp = fixtures["self_success_checked"]

		res, err := adp.CheckIn(context.Background(), ch)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Success || !res.Already {
			t.Errorf("expected success with already=true, got %+v", res)
		}
	})

	t.Run("Balance", func(t *testing.T) {
		bal, err := adp.Balance(context.Background(), ch)
		if err != nil {
			t.Fatalf("Balance failed: %v", err)
		}
		if bal.Remaining != 35000000 {
			t.Errorf("expected remaining 35000000, got %d", bal.Remaining)
		}
		if bal.AvailableUSD != 70.0 { // 35000000 / 500000 = 70.0
			t.Errorf("expected AvailableUSD 70.0, got %f", bal.AvailableUSD)
		}
	})
}

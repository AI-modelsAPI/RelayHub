package seekai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"relayhub/internal/adapter"
	"relayhub/internal/adapter/testhelper"
	"relayhub/internal/domain"
)

func TestSeekAI_All(t *testing.T) {
	fixturePath := filepath.Join("..", "fixtures", "seekai", "responses.json")
	fixtures, err := testhelper.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("failed to load fixtures: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify CredentialRef is NEVER sent as Bearer token
		authHeader := r.Header.Get("Authorization")
		if authHeader == "Bearer cred-seek-1" {
			t.Errorf("SECURITY VIOLATION: CredentialRef sent as Bearer token: %s", authHeader)
		}

		switch r.URL.Path {
		case "/api/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fixtures["status_success"])
		case "/api/user/self":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fixtures["self_success"])
		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	secStore := testhelper.NewMockSecretStore()
	_ = secStore.Put(context.Background(), "cred-seek-1", []byte(`{"token":"seekai-valid-token"}`))

	adp := NewWithSecrets(srv.Client(), secStore)
	ch := domain.Channel{
		ID:            "ch-seek",
		BaseURL:       srv.URL,
		CredentialRef: "cred-seek-1",
	}

	t.Run("CheckIn_MustRequireTurnstileWebview", func(t *testing.T) {
		res, err := adp.CheckIn(context.Background(), ch)
		if res.Success {
			t.Fatalf("CRITICAL BUG: SeekAI CheckIn must never succeed in pure HTTP without Turnstile!")
		}
		if !res.NeedWebview {
			t.Errorf("expected NeedWebview: true")
		}
		if !errors.Is(err, adapter.ErrNeedWebview) {
			t.Errorf("expected ErrNeedWebview, got: %v", err)
		}
	})

	t.Run("Balance", func(t *testing.T) {
		bal, err := adp.Balance(context.Background(), ch)
		if err != nil {
			t.Fatalf("Balance failed: %v", err)
		}
		if bal.Remaining != 12000000 {
			t.Errorf("expected remaining 12000000, got %d", bal.Remaining)
		}
		if bal.AvailableUSD != 24.0 { // 12000000 / 500000 = 24.0
			t.Errorf("expected AvailableUSD 24.0, got %f", bal.AvailableUSD)
		}
	})
}

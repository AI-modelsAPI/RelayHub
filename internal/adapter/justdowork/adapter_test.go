package justdowork

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"relayhub/internal/adapter"
	"relayhub/internal/adapter/testhelper"
	"relayhub/internal/domain"
)

func TestJustDoWork_All(t *testing.T) {
	fixturePath := filepath.Join("..", "fixtures", "justdowork", "responses.json")
	fixtures, err := testhelper.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("failed to load fixtures: %v", err)
	}

	var refreshCalled int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify CredentialRef is NEVER sent as Bearer token
		authHeader := r.Header.Get("Authorization")
		if authHeader == "Bearer cred-just-1" {
			t.Errorf("SECURITY VIOLATION: CredentialRef sent as Bearer token: %s", authHeader)
		}

		switch r.URL.Path {
		case "/api/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fixtures["status_success"])
		case "/api/user/self":
			// If initial token is expired or unauthorized, trigger 401
			if authHeader == "Bearer old-expired-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"token expired"}}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fixtures["self_success"])
		case "/api/user/auth/refresh":
			atomic.AddInt32(&refreshCalled, 1)
			w.Header().Add("Set-Cookie", "new_api_refresh=rotated-token-cookie; Path=/")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fixtures["refresh_success"])
		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	secStore := testhelper.NewMockSecretStore()
	initialCred := `{"token":"old-expired-token","site_cookie":"new_api_refresh=initial-cookie; session=123"}`
	_ = secStore.Put(context.Background(), "cred-just-1", []byte(initialCred))

	adp := NewWithSecrets(srv.Client(), secStore)
	ch := domain.Channel{
		ID:            "ch-just",
		BaseURL:       srv.URL,
		CredentialRef: "cred-just-1",
	}

	t.Run("CheckIn_MustRequireTurnstileWebview", func(t *testing.T) {
		res, err := adp.CheckIn(context.Background(), ch)
		if res.Success {
			t.Fatalf("CRITICAL BUG: JustDoWork CheckIn must never succeed in pure HTTP without Turnstile!")
		}
		if !res.NeedWebview {
			t.Errorf("expected NeedWebview: true")
		}
		if !errors.Is(err, adapter.ErrNeedWebview) {
			t.Errorf("expected ErrNeedWebview, got: %v", err)
		}
	})

	t.Run("Refresh_And_Balance_AutoRenewOn401", func(t *testing.T) {
		bal, err := adp.Balance(context.Background(), ch)
		if err != nil {
			t.Fatalf("Balance failed: %v", err)
		}
		if atomic.LoadInt32(&refreshCalled) == 0 {
			t.Errorf("expected Refresh to have been triggered by 401, got 0 calls")
		}
		if bal.Remaining != 45000000 {
			t.Errorf("expected remaining 45000000, got %d", bal.Remaining)
		}
		if bal.AvailableUSD != 90.0 { // 45000000 / 500000 = 90.0
			t.Errorf("expected 90.0 USD, got %f", bal.AvailableUSD)
		}
	})
}

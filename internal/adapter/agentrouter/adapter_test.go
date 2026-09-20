package agentrouter

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

func TestAgentRouter_All(t *testing.T) {
	fixturePath := filepath.Join("..", "fixtures", "agentrouter", "responses.json")
	commonErrPath := filepath.Join("..", "fixtures", "common_errors.json")
	fixtures, err := testhelper.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("failed to load fixtures: %v", err)
	}
	commonErr, err := testhelper.LoadFixture(commonErrPath)
	if err != nil {
		t.Fatalf("failed to load common errors: %v", err)
	}

	var statusResp []byte = fixtures["status_success"]
	var selfResp []byte = fixtures["self_success"]
	var checkinResp []byte = fixtures["checkin_status_not_checked"]
	var logResp []byte = fixtures["log_bonus_today"]
	var affResp []byte = fixtures["aff_transfer_success"]
	var usageResp []byte = fixtures["usage_today"]

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Bearer token is NEVER the raw credential reference string "cred-ar-1"
		authHeader := r.Header.Get("Authorization")
		if authHeader == "Bearer cred-ar-1" {
			t.Errorf("SECURITY VIOLATION: CredentialRef was sent as Bearer token: %s", authHeader)
		}

		switch r.URL.Path {
		case "/api/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(statusResp)
		case "/api/user/self":
			if authHeader == "Bearer invalid-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write(commonErr["http_401_unauthorized"])
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(selfResp)
		case "/api/user/checkin":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(checkinResp)
		case "/api/log/self":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(logResp)
		case "/api/user/aff_transfer":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(affResp)
		case "/api/data/self":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(usageResp)
		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	secStore := testhelper.NewMockSecretStore()
	_ = secStore.Put(context.Background(), "cred-ar-1", []byte(`{"token":"ar-real-secret-token","site_cookie":"session=abcdef"}`))

	adp := NewWithSecrets(srv.Client(), secStore)

	ch := domain.Channel{
		ID:            "ch-ar",
		BaseURL:       srv.URL,
		CredentialRef: "cred-ar-1",
	}

	t.Run("Validate", func(t *testing.T) {
		if err := adp.Validate(context.Background(), ch); err != nil {
			t.Fatalf("expected valid, got: %v", err)
		}
		if err := adp.Validate(context.Background(), domain.Channel{}); err == nil {
			t.Fatalf("expected invalid for empty channel")
		}
	})

	t.Run("Balance", func(t *testing.T) {
		bal, err := adp.Balance(context.Background(), ch)
		if err != nil {
			t.Fatalf("Balance failed: %v", err)
		}
		if bal.Remaining != 10000000 {
			t.Errorf("expected remaining 10000000, got %d", bal.Remaining)
		}
		if bal.AvailableUSD != 20.0 { // 10000000 / 500000 = 20.0
			t.Errorf("expected AvailableUSD 20.0, got %f", bal.AvailableUSD)
		}
		if bal.TodayUsedUSD != 0.5 { // (100000 + 150000) / 500000 = 0.5
			t.Errorf("expected TodayUsedUSD 0.5, got %f", bal.TodayUsedUSD)
		}
	})

	t.Run("CheckIn_WithTodayBonusInLog", func(t *testing.T) {
		// Log has today's checkin bonus
		res, err := adp.CheckIn(context.Background(), ch)
		if err != nil {
			t.Fatalf("CheckIn returned error: %v", err)
		}
		if !res.Success || !res.Already {
			t.Errorf("expected success with already=true, got %+v", res)
		}
		if res.RewardUSD != 25.0 {
			t.Errorf("expected reward USD 25.0, got %f", res.RewardUSD)
		}
	})

	t.Run("CheckIn_NeverFalselyReportsSuccess", func(t *testing.T) {
		// When no log bonus and no calendar record, AgentRouter must return NeedsRelogin=true, Success=false
		logResp = fixtures["log_bonus_yesterday"]
		checkinResp = fixtures["checkin_status_not_checked"]

		res, err := adp.CheckIn(context.Background(), ch)
		if res.Success {
			t.Fatalf("CRITICAL BUG: CheckIn reported Success: true without today's checkin record!")
		}
		if !res.NeedsRelogin {
			t.Errorf("expected NeedsRelogin to be true")
		}
		if !errors.Is(err, adapter.ErrNeedsRelogin) {
			t.Errorf("expected ErrNeedsRelogin, got %v", err)
		}
	})

	t.Run("AffTransfer", func(t *testing.T) {
		ok, msg, err := adp.AffTransfer(context.Background(), ch)
		if err != nil || !ok {
			t.Fatalf("AffTransfer failed: ok=%v, msg=%s, err=%v", ok, msg, err)
		}
	})

	t.Run("Auth401Failure", func(t *testing.T) {
		_ = secStore.Put(context.Background(), "cred-bad", []byte(`{"token":"invalid-token"}`))
		badCh := domain.Channel{BaseURL: srv.URL, CredentialRef: "cred-bad"}
		_, err := adp.Balance(context.Background(), badCh)
		if err == nil {
			t.Fatalf("expected 401 error, got nil")
		}
	})
}

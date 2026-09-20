package adapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"relayhub/internal/adapter/testhelper"
	"relayhub/internal/domain"
)

func TestBaseNewAPIAdapter_ErrorsAndEdgeCases(t *testing.T) {
	var statusCode int
	var respBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		_, _ = w.Write(respBody)
	}))
	defer srv.Close()

	secStore := testhelper.NewMockSecretStore()
	_ = secStore.Put(context.Background(), "cred-test", []byte(`{"token":"valid-token"}`))

	adp := &BaseNewAPIAdapter{
		Client:      srv.Client(),
		Secrets:     secStore,
		CheckinType: "login",
	}
	ch := domain.Channel{
		ID:            "ch-err-test",
		BaseURL:       srv.URL,
		CredentialRef: "cred-test",
	}

	t.Run("HTTP_403_Forbidden", func(t *testing.T) {
		statusCode = http.StatusForbidden
		respBody = []byte(`{"error":{"message":"forbidden"}}`)
		_, err := adp.Balance(context.Background(), ch)
		if err == nil {
			t.Fatalf("expected error on 403, got nil")
		}
	})

	t.Run("HTTP_429_RateLimit", func(t *testing.T) {
		statusCode = http.StatusTooManyRequests
		respBody = []byte(`{"error":{"message":"rate limit exceeded"}}`)
		_, err := adp.Balance(context.Background(), ch)
		if err == nil {
			t.Fatalf("expected error on 429, got nil")
		}
	})

	t.Run("HTTP_500_InternalServerError", func(t *testing.T) {
		statusCode = http.StatusInternalServerError
		respBody = []byte(`{"error":{"message":"internal error"}}`)
		_, err := adp.Balance(context.Background(), ch)
		if err == nil {
			t.Fatalf("expected error on 500, got nil")
		}
	})

	t.Run("MissingCredentialRef", func(t *testing.T) {
		badCh := domain.Channel{BaseURL: srv.URL, CredentialRef: ""}
		_, err := adp.Balance(context.Background(), badCh)
		if !errors.Is(err, ErrSecretRequired) {
			t.Errorf("expected ErrSecretRequired, got %v", err)
		}
	})

	t.Run("SecretNotFoundInStore", func(t *testing.T) {
		badCh := domain.Channel{BaseURL: srv.URL, CredentialRef: "non-existent-ref"}
		_, err := adp.Balance(context.Background(), badCh)
		if err == nil {
			t.Fatalf("expected error when secret not found")
		}
	})

	t.Run("MalformattedResponseBody", func(t *testing.T) {
		statusCode = http.StatusOK
		respBody = []byte(`not-a-json-object`)
		_, err := adp.Balance(context.Background(), ch)
		if err == nil {
			t.Fatalf("expected error on malformatted JSON")
		}
	})
}

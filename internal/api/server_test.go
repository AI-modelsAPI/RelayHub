package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relayhub/internal/auth"
	"relayhub/internal/domain"
	"relayhub/internal/logging"
	"relayhub/internal/repository"
	"relayhub/internal/secrets"
	"relayhub/internal/storage"
)

func testServer(t *testing.T, token string) http.Handler {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "relayhub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := NewConfiguredServer(Config{Token: token, Repo: repository.NewSQLite(db.DB), LocalOnly: true, Management: "127.0.0.1:8790"})
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler()
}

func request(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var input *bytes.Reader
	if body == "" {
		input = bytes.NewReader(nil)
	} else {
		input = bytes.NewReader([]byte(body))
	}
	r := httptest.NewRequest(method, path, input)
	r.Header.Set("X-Request-ID", "test-request-42")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHealthAndRequestID(t *testing.T) {
	h := NewServer("").Handler()
	w := request(t, h, http.MethodGet, "/healthz", "", "")
	if w.Code != http.StatusOK || w.Header().Get("X-Request-ID") != "test-request-42" {
		t.Fatalf("status=%d headers=%v", w.Code, w.Header())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got["status"] != "ok" {
		t.Fatalf("body=%s err=%v", w.Body.String(), err)
	}
}

func TestManagementAuthAndDeterministicErrorEnvelope(t *testing.T) {
	h := testServer(t, "secret")
	w := request(t, h, http.MethodPost, "/api/v1/providers", "", `{"id":"p"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
	var envelope struct {
		Error Error `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "authentication_error" || envelope.Error.RequestID != "test-request-42" {
		t.Fatalf("envelope=%+v", envelope.Error)
	}

	w = request(t, h, http.MethodPost, "/api/v1/providers", "secret", `{bad`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got malformed status %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "invalid_json" || envelope.Error.RequestID != "test-request-42" {
		t.Fatalf("envelope=%+v", envelope.Error)
	}
}

func TestConfiguredServerRejectsNonLoopbackManagement(t *testing.T) {
	if _, err := NewConfiguredServer(Config{LocalOnly: true, Management: "0.0.0.0:8790"}); err == nil {
		t.Fatal("expected non-loopback address rejection")
	}
	if _, err := NewConfiguredServer(Config{LocalOnly: true, Management: "127.0.0.1:8790"}); err != nil {
		t.Fatal(err)
	}
}

func TestResourceCRUDAndRelationshipValidation(t *testing.T) {
	h := testServer(t, "secret")
	post := func(path, body string) {
		w := request(t, h, http.MethodPost, path, "secret", body)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("POST %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	post("/api/v1/providers", `{"id":"p1","name":"Provider","adapter_type":"generic","protocol":"openai-chat","base_url_template":"https://upstream.invalid","enabled":true}`)
	post("/api/v1/channels", `{"id":"c1","provider_id":"p1","name":"Account A","base_url":"https://upstream.invalid","credential_ref":"cred:c1","enabled":true}`)
	post("/api/v1/models", `{"id":"m1","display_name":"Logical Model","aliases":["alias"],"enabled":true}`)
	post("/api/v1/provider-models", `{"id":"pm1","provider_id":"p1","channel_id":"c1","model_id":"m1","upstream_model_name":"upstream-model","protocol":"openai-chat","enabled":true}`)
	post("/api/v1/model-groups", `{"id":"g1","name":"Coding","strategy":"priority","enabled":true}`)
	post("/api/v1/model-groups/g1/members", `{"model_id":"m1","priority":1,"weight":1}`)
	post("/api/v1/routes", `{"id":"r1","name":"Default","protocol":"openai-chat","model_pattern":"m1","strategy":"priority","enabled":true}`)

	for _, path := range []string{"providers", "channels", "models", "provider-models", "model-groups", "routes"} {
		w := request(t, h, http.MethodGet, "/api/v1/"+path, "secret", "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := request(t, h, http.MethodGet, "/api/v1/model-groups/g1", "secret", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "m1") {
		t.Fatalf("group=%d %s", w.Code, w.Body.String())
	}

	w = request(t, h, http.MethodPut, "/api/v1/providers/p1", "secret", `{"name":"Provider Updated","protocol":"openai-chat","adapter_type":"generic","enabled":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update provider: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodDelete, "/api/v1/providers/p1", "secret", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("referenced delete: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodDelete, "/api/v1/routes/r1", "secret", "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete route: %d %s", w.Code, w.Body.String())
	}
}

func TestSecretFieldsAreWriteOnlyAndUnknownFieldsRejected(t *testing.T) {
	h := testServer(t, "secret")
	w := request(t, h, http.MethodPost, "/api/v1/providers", "secret", `{"id":"p","name":"P","protocol":"openai-chat","api_key":"do-not-store"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown secret field accepted: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodPost, "/api/v1/providers", "secret", `{"id":"p","name":"P","protocol":"openai-chat","base_url_template":"https://example.invalid"}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "do-not-store") {
		t.Fatalf("secret leaked: %d %s", w.Code, w.Body.String())
	}
}

type failingRepository struct {
	repository.ResourceRepository
}

func (failingRepository) ListProviders(context.Context) ([]domain.Provider, error) {
	return nil, fmt.Errorf("sentinel-secret-path")
}
func TestRemoteManagementRequiresCompletePolicy(t *testing.T) {
	for _, cfg := range []Config{
		{LocalOnly: false, Management: "0.0.0.0:8790"},
		{LocalOnly: false, Management: "0.0.0.0:8790", Token: "token"},
		{LocalOnly: false, Management: "0.0.0.0:8790", AllowedCIDRs: []string{"127.0.0.1/32"}, RateLimit: RateLimitConfig{Requests: 1, Window: time.Minute}},
	} {
		if _, err := NewConfiguredServer(cfg); err == nil {
			t.Fatal("incomplete remote policy was accepted")
		}
	}
}

func TestManagementListenerPeerAndRatePolicy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server, err := NewServerOnListener(listener, Config{LocalOnly: false, Management: "127.0.0.1:0", Token: "token", AllowedCIDRs: []string{"127.0.0.1/32"}, RateLimit: RateLimitConfig{Requests: 1, Window: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer token")
	first := httptest.NewRecorder()
	server.Handler.ServeHTTP(first, request)
	if first.Code != http.StatusOK {
		t.Fatalf("first request: %d %s", first.Code, first.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer token")
	second := httptest.NewRecorder()
	server.Handler.ServeHTTP(second, request)
	request = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "10.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer token")
	remote := httptest.NewRecorder()
	server.Handler.ServeHTTP(remote, request)
	if remote.Code != http.StatusForbidden {
		t.Fatalf("non-allowlisted peer: %d %s", remote.Code, remote.Body.String())
	}
}

func TestInternalErrorsAreGenericAndLoggedWithRequestID(t *testing.T) {
	var logs bytes.Buffer
	stub := &failingRepository{}
	s, err := NewConfiguredServer(Config{Token: "token", Repo: stub, LocalOnly: true, Management: "127.0.0.1:8790", Logger: logging.New(&logs)})
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, s.Handler(), http.MethodGet, "/api/v1/providers", "token", "")
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "sentinel-secret-path") || !strings.Contains(w.Body.String(), "internal management error") {
		t.Fatalf("response leaked or wrong: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(logs.String(), "test-request-42") || !strings.Contains(logs.String(), "sentinel-secret-path") {
		t.Fatalf("diagnostic was not logged: %s", logs.String())
	}
}

func TestSecretWriteUsesEncryptedStoreWithoutEcho(t *testing.T) {
	key, err := secrets.NewStaticKeyProvider(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := secrets.NewTestStore(key)
	s, err := NewConfiguredServer(Config{Token: "token", LocalOnly: true, Management: "127.0.0.1:8790", SecretStore: store})
	if err != nil {
		t.Fatal(err)
	}
	plain := "plaintext-secret-value"
	w := request(t, s.Handler(), http.MethodPost, "/api/v1/secrets", "token", fmt.Sprintf(`{"ref":"credential-1","value":%q}`, plain))
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), plain) {
		t.Fatalf("secret response: %d %s", w.Code, w.Body.String())
	}
	got, err := store.Get(context.Background(), "credential-1")
	if err != nil || string(got) != plain {
		t.Fatalf("secret readback: %q %v", got, err)
	}
}
func TestAPIKeysManagementEndpoint(t *testing.T) {
	keys := auth.NewLocalKeyService()
	cfg := Config{
		Token:      "secret",
		LocalOnly:  true,
		Management: "127.0.0.1:8790",
		LocalKeys:  keys,
	}
	s, err := NewConfiguredServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	// 1. Create key
	w := request(t, h, http.MethodPost, "/api/v1/keys", "secret", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("create key expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Key string `json:"key"`
		ID  string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Key == "" || created.ID == "" {
		t.Fatalf("expected key and id in response, got %+v", created)
	}
	if !keys.Validate(created.Key) {
		t.Fatalf("created key is not valid")
	}

	// 2. List keys
	w = request(t, h, http.MethodGet, "/api/v1/keys", "secret", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list keys expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Keys []struct {
			ID string `json:"id"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Keys) != 1 || listResp.Keys[0].ID != created.ID {
		t.Fatalf("expected 1 key with id %s, got %+v", created.ID, listResp.Keys)
	}
	// Verify raw key never leaks in listing
	if strings.Contains(w.Body.String(), created.Key) {
		t.Fatalf("raw key leaked in list response: %s", w.Body.String())
	}

	// 3. Revoke key
	w = request(t, h, http.MethodDelete, "/api/v1/keys/"+created.ID, "secret", "")
	if w.Code != http.StatusOK {
		t.Fatalf("revoke key expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if keys.Validate(created.Key) {
		t.Fatalf("key was not revoked")
	}
}

func TestUnsupportedMutationNeverClaimsSuccess(t *testing.T) {
	h := testServer(t, "secret")
	for _, path := range []string{"/api/v1/checkin", "/api/v1/logs", "/api/v1/settings"} {
		w := request(t, h, http.MethodPost, path, "secret", `{}`)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("%s: got %d %s", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"status":"success"`) {
			t.Fatalf("unsupported mutation claimed success: %s", w.Body.String())
		}
	}
}

// cli-sync and import-export are now implemented (P1-6 / P1-7), so they no
// longer answer 501. The invariant they must still honour is the important
// half of the original assertion: a request that performs no mutation must
// never report success.
func TestImplementedMutationsRejectBadInputWithoutClaimingSuccess(t *testing.T) {
	h := testServer(t, "secret")
	cases := []struct {
		path string
		body string
		why  string
	}{
		{"/api/v1/cli-sync", `{}`, "no cli named"},
		{"/api/v1/cli-sync", `{"cli":"hermes","action":"bogus"}`, "unknown action"},
		{"/api/v1/cli-sync", `{"cli":"nope","action":"preview"}`, "unknown cli"},
		{"/api/v1/import-export", `{}`, "no action named"},
		{"/api/v1/import-export", `{"action":"preview"}`, "no package supplied"},
		{"/api/v1/import-export", `{"action":"apply","package":"{\"manifest\":{\"version\":1}}"}`, "tampered package"},
	}
	for _, tc := range cases {
		w := request(t, h, http.MethodPost, tc.path, "secret", tc.body)
		if w.Code < 400 {
			t.Fatalf("%s (%s): expected a client/server error, got %d %s", tc.path, tc.why, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"status":"success"`) {
			t.Fatalf("%s (%s): rejected request claimed success: %s", tc.path, tc.why, w.Body.String())
		}
	}
}

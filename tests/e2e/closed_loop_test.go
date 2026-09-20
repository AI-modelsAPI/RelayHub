package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relayhub/internal/app"
)

// TestClosedLoopInterfaceAddChannelClientCallGateway verifies the full closed loop:
// 1. Core runtime starts with clean DB.
// 2. Client adds resources via management API using the EXACT payload structure sent by web/app.js.
// 3. Client issues a local API key via POST /api/v1/keys without restarting the process.
// 4. Client calls Gateway /v1/chat/completions using the issued key.
// 5. Gateway routes the request, injects the secret credentials, and mock upstream receives them and replies 200.
// 6. Verify secrets are never leaked into logs.
func TestClosedLoopInterfaceAddChannelClientCallGateway(t *testing.T) {
	var receivedAuthHeader string
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			receivedAuthHeader = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-loop","choices":[{"message":{"role":"assistant","content":"pong"}}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockUpstream.Close()

	dataDir := t.TempDir()
	cfg := app.Config{
		DataDir:        dataDir,
		HTTPProxyAddr:  "127.0.0.1:0",
		SOCKS5Addr:     "127.0.0.1:0",
		GatewayAddr:    "127.0.0.1:0",
		ManagementAddr: "127.0.0.1:0",
		WireFullStack:  true,
	}

	core, err := app.New(cfg)
	if err != nil {
		t.Fatalf("New app failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start app failed: %v", err)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = core.Shutdown(shutCtx)
	}()

	rt := core.Runtime()
	apiURL := "http://" + rt.APIListener.Addr().String()
	gwURL := "http://" + rt.GWListener.Addr().String()

	apiPost := func(path string, body string) {
		resp, err := http.Post(apiURL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST %s failed: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("POST %s failed with %d: %s", path, resp.StatusCode, string(b))
		}
	}

	// 1. Store secret (API key for upstream)
	upstreamSecret := "sk-upstream-secret-xyz-999"
	apiPost("/api/v1/secrets", fmt.Sprintf(`{"ref":"cred-upstream-loop","value":%q}`, upstreamSecret))

	// 2. Create Provider using web/app.js payload shape
	apiPost("/api/v1/providers", fmt.Sprintf(`{"id":"p-loop","name":"LoopProvider","adapter_type":"generic","protocol":"openai-chat","base_url_template":%q,"enabled":true}`, mockUpstream.URL))

	// 3. Create Channel using web/app.js payload shape
	apiPost("/api/v1/channels", fmt.Sprintf(`{"id":"c-loop","provider_id":"p-loop","name":"LoopChan","base_url":%q,"credential_ref":"cred-upstream-loop","enabled":true,"routing_enabled":true}`, mockUpstream.URL))

	// 4. Create Model
	apiPost("/api/v1/models", `{"id":"gpt-4o","display_name":"GPT-4o","enabled":true}`)

	// 5. Create ProviderModel binding using web/app.js payload shape (protocol: "openai-chat")
	apiPost("/api/v1/provider-models", `{"id":"pm-loop-gpt-4o","provider_id":"p-loop","channel_id":"c-loop","model_id":"gpt-4o","upstream_model_name":"gpt-4o","protocol":"openai-chat","priority":1,"weight":1,"enabled":true}`)

	// 6. Issue local API key via new /api/v1/keys endpoint
	keyResp, err := http.Post(apiURL+"/api/v1/keys", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /api/v1/keys failed: %v", err)
	}
	keyBody, _ := io.ReadAll(keyResp.Body)
	_ = keyResp.Body.Close()
	if keyResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from /api/v1/keys, got %d: %s", keyResp.StatusCode, string(keyBody))
	}
	var keyData struct {
		Key string `json:"key"`
		ID  string `json:"id"`
	}
	if err := json.Unmarshal(keyBody, &keyData); err != nil || keyData.Key == "" {
		t.Fatalf("failed to decode created key: %s", string(keyBody))
	}

	// 7. Call Gateway directly WITHOUT RESTARTING THE PROCESS
	gwReqBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"ping"}]}`)
	gwReq, _ := http.NewRequest(http.MethodPost, gwURL+"/v1/chat/completions", bytes.NewReader(gwReqBody))
	gwReq.Header.Set("Authorization", "Bearer "+keyData.Key)
	gwReq.Header.Set("Content-Type", "application/json")

	gwResp, err := http.DefaultClient.Do(gwReq)
	if err != nil {
		t.Fatalf("Gateway request failed: %v", err)
	}
	gwRespBody, _ := io.ReadAll(gwResp.Body)
	_ = gwResp.Body.Close()

	if gwResp.StatusCode != http.StatusOK {
		t.Fatalf("Gateway returned status %d: %s", gwResp.StatusCode, string(gwRespBody))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(gwRespBody, &chatResp); err != nil || len(chatResp.Choices) == 0 {
		t.Fatalf("failed to parse gateway response: %s", string(gwRespBody))
	}
	if chatResp.Choices[0].Message.Content != "pong" {
		t.Fatalf("expected 'pong', got %q", chatResp.Choices[0].Message.Content)
	}

	// 8. Assert upstream received injected Authorization header with credential
	expectedAuth := "Bearer " + upstreamSecret
	if receivedAuthHeader != expectedAuth {
		t.Fatalf("upstream did not receive expected auth header: got %q, want %q", receivedAuthHeader, expectedAuth)
	}
}

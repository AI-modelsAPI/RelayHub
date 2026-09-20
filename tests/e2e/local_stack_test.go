package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"relayhub/internal/app"
)

func TestLocalStackE2E(t *testing.T) {
	// 1. Mock upstream AI service
	var receivedUpstreamAuth string
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			receivedUpstreamAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chat-123","choices":[{"message":{"content":"hello from mock upstream"}}]}`))
		case "/v1/messages":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"msg-123","content":[{"type":"text","text":"hello from anthropic mock"}]}`))
		default:
			_, _ = w.Write([]byte("mock response"))
		}
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
	proxyAddr := rt.HTTPProxy.Addr().String()

	// 2. Provision Resources via Management API (simulating web/app.js payloads)
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

	apiPost("/api/v1/secrets", `{"ref":"cred-upstream","value":"secret-upstream-token"}`)
	apiPost("/api/v1/providers", fmt.Sprintf(`{"id":"p-mock","name":"MockProvider","adapter_type":"generic","protocol":"openai-chat","base_url_template":%q,"enabled":true}`, mockUpstream.URL))
	apiPost("/api/v1/channels", fmt.Sprintf(`{"id":"c-mock","provider_id":"p-mock","name":"MockChan","base_url":%q,"credential_ref":"cred-upstream","enabled":true,"routing_enabled":true}`, mockUpstream.URL))
	apiPost("/api/v1/models", `{"id":"m-chat","display_name":"MockModel","enabled":true}`)
	apiPost("/api/v1/provider-models", `{"id":"pm-1","provider_id":"p-mock","channel_id":"c-mock","model_id":"m-chat","upstream_model_name":"gpt-mock","protocol":"openai-chat","enabled":true}`)

	// 3. Create a Local API Key via Management API
	keyResp, err := http.Post(apiURL+"/api/v1/keys", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("failed to create key via management API: %v", err)
	}
	keyBody, _ := io.ReadAll(keyResp.Body)
	_ = keyResp.Body.Close()
	if keyResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from /api/v1/keys, got %d: %s", keyResp.StatusCode, string(keyBody))
	}
	var createdKey struct {
		Key string `json:"key"`
		ID  string `json:"id"`
	}
	if err := json.Unmarshal(keyBody, &createdKey); err != nil || createdKey.Key == "" {
		t.Fatalf("invalid key response: %s", string(keyBody))
	}
	apiKey := createdKey.Key

	// 4. Test Gateway OpenAI chat completions
	chatReqBody := []byte(`{"model":"m-chat","messages":[{"role":"user","content":"hi"}]}`)
	gwReq, _ := http.NewRequest(http.MethodPost, gwURL+"/v1/chat/completions", bytes.NewReader(chatReqBody))
	gwReq.Header.Set("Authorization", "Bearer "+apiKey)
	gwReq.Header.Set("Content-Type", "application/json")

	gwResp, err := http.DefaultClient.Do(gwReq)
	if err != nil {
		t.Fatalf("gateway call failed: %v", err)
	}
	gwRespBody, _ := io.ReadAll(gwResp.Body)
	_ = gwResp.Body.Close()

	if gwResp.StatusCode != http.StatusOK {
		t.Fatalf("gateway returned status %d: %s", gwResp.StatusCode, string(gwRespBody))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(gwRespBody, &chatResp); err != nil || len(chatResp.Choices) == 0 {
		t.Fatalf("unexpected gateway response: %s", string(gwRespBody))
	}
	if chatResp.Choices[0].Message.Content != "hello from mock upstream" {
		t.Fatalf("unexpected choice content: %s", chatResp.Choices[0].Message.Content)
	}
	if receivedUpstreamAuth != "Bearer secret-upstream-token" {
		t.Fatalf("upstream did not receive injected credential header: got %q", receivedUpstreamAuth)
	}

	// 5. Test Transparent HTTP Proxying
	proxyClient := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr}),
		},
	}
	pResp, err := proxyClient.Get(mockUpstream.URL + "/direct-test")
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	pBody, _ := io.ReadAll(pResp.Body)
	_ = pResp.Body.Close()
	if string(pBody) != "mock response" {
		t.Fatalf("unexpected proxy response: %s", string(pBody))
	}

	// 6. Test Management API Healthz & Overview
	mgmtResp, err := http.Get(apiURL + "/healthz")
	if err != nil || mgmtResp.StatusCode != http.StatusOK {
		t.Fatalf("management healthz failed: %v", err)
	}
	_ = mgmtResp.Body.Close()

	mgmtOverview, err := http.Get(apiURL + "/api/v1/overview")
	if err != nil || mgmtOverview.StatusCode != http.StatusOK {
		t.Fatalf("management overview failed: %v", err)
	}
	_ = mgmtOverview.Body.Close()
}

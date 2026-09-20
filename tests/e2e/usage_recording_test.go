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
	"relayhub/internal/domain"
)

func TestUsageRecordingEndToEnd(t *testing.T) {
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-usage-test",
				"choices":[{"message":{"role":"assistant","content":"hello"}}],
				"usage":{"prompt_tokens":15,"completion_tokens":25,"total_tokens":40}
			}`))
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

	// 1. Setup provider, channel, model, provider_model
	apiPost("/api/v1/providers", fmt.Sprintf(`{"id":"p-usage","name":"UsageProvider","adapter_type":"generic","protocol":"openai-chat","base_url_template":%q,"enabled":true}`, mockUpstream.URL))
	apiPost("/api/v1/channels", fmt.Sprintf(`{"id":"c-usage","provider_id":"p-usage","name":"UsageChan","base_url":%q,"enabled":true,"routing_enabled":true}`, mockUpstream.URL))
	apiPost("/api/v1/models", `{"id":"gpt-usage","display_name":"GPT-Usage","enabled":true}`)
	apiPost("/api/v1/provider-models", `{"id":"pm-usage","provider_id":"p-usage","channel_id":"c-usage","model_id":"gpt-usage","upstream_model_name":"gpt-usage","protocol":"openai-chat","priority":1,"weight":1,"enabled":true}`)

	// 2. Issue local key
	keyResp, err := http.Post(apiURL+"/api/v1/keys", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /api/v1/keys: %v", err)
	}
	var keyData struct {
		Key string `json:"key"`
	}
	_ = json.NewDecoder(keyResp.Body).Decode(&keyData)
	keyResp.Body.Close()

	// 3. Make gateway request
	gwReqBody := []byte(`{"model":"gpt-usage","messages":[{"role":"user","content":"hi"}]}`)
	gwReq, _ := http.NewRequest(http.MethodPost, gwURL+"/v1/chat/completions", bytes.NewReader(gwReqBody))
	gwReq.Header.Set("Authorization", "Bearer "+keyData.Key)
	gwReq.Header.Set("Content-Type", "application/json")
	gwReq.Header.Set("X-Request-ID", "req-usage-123")

	gwResp, err := http.DefaultClient.Do(gwReq)
	if err != nil {
		t.Fatalf("Gateway request failed: %v", err)
	}
	defer gwResp.Body.Close()
	if gwResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(gwResp.Body)
		t.Fatalf("Gateway status %d: %s", gwResp.StatusCode, string(b))
	}

	// 4. Query /api/v1/usage
	usageResp, err := http.Get(apiURL + "/api/v1/usage")
	if err != nil {
		t.Fatalf("GET /api/v1/usage failed: %v", err)
	}
	defer usageResp.Body.Close()
	if usageResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/usage returned %d", usageResp.StatusCode)
	}

	var usageResult struct {
		Records []domain.RequestRecord `json:"records"`
		Summary struct {
			TotalRequests int `json:"total_requests"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(usageResp.Body).Decode(&usageResult); err != nil {
		t.Fatalf("decode /api/v1/usage: %v", err)
	}

	if len(usageResult.Records) != 1 {
		t.Fatalf("expected 1 usage record, got %d (records=%+v)", len(usageResult.Records), usageResult.Records)
	}

	rec := usageResult.Records[0]
	if rec.InputTokens != 15 || rec.OutputTokens != 25 {
		t.Fatalf("expected input=15 output=25 tokens, got input=%d output=%d", rec.InputTokens, rec.OutputTokens)
	}
	if rec.StatusCode != 200 {
		t.Fatalf("expected status_code=200, got %d", rec.StatusCode)
	}
	if rec.ModelID != "gpt-usage" {
		t.Fatalf("expected model_id='gpt-usage', got %q", rec.ModelID)
	}

	// Verify specification §11.1: records must only contain metadata, no prompt/request/response body
	rawJSON, _ := json.Marshal(rec)
	recStr := string(rawJSON)
	for _, forbidden := range []string{"messages", "choices", "hello", "hi", "prompt"} {
		if strings.Contains(strings.ToLower(recStr), forbidden) {
			t.Fatalf("usage record contains forbidden content %q: %s", forbidden, recStr)
		}
	}
}

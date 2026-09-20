package e2e

import (
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

func TestGetV1ModelsReturnsRoutableModels(t *testing.T) {
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
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

	// 1. Setup provider, channel, models
	apiPost("/api/v1/providers", fmt.Sprintf(`{"id":"p-models","name":"ModelsProvider","adapter_type":"generic","protocol":"openai-chat","base_url_template":%q,"enabled":true}`, mockUpstream.URL))
	apiPost("/api/v1/channels", fmt.Sprintf(`{"id":"c-models","provider_id":"p-models","name":"ModelsChan","base_url":%q,"enabled":true,"routing_enabled":true}`, mockUpstream.URL))
	apiPost("/api/v1/models", `{"id":"claude-3-7-sonnet","display_name":"Claude 3.7 Sonnet","enabled":true}`)
	apiPost("/api/v1/models", `{"id":"gpt-4o","display_name":"GPT-4o","enabled":true}`)
	apiPost("/api/v1/models", `{"id":"disabled-model","display_name":"Disabled","enabled":false}`)

	// Bind claude-3-7-sonnet and gpt-4o to active channel
	apiPost("/api/v1/provider-models", `{"id":"pm-claude","provider_id":"p-models","channel_id":"c-models","model_id":"claude-3-7-sonnet","upstream_model_name":"claude-3-7-sonnet","protocol":"openai-chat","priority":1,"weight":1,"enabled":true}`)
	apiPost("/api/v1/provider-models", `{"id":"pm-gpt4o","provider_id":"p-models","channel_id":"c-models","model_id":"gpt-4o","upstream_model_name":"gpt-4o","protocol":"openai-chat","priority":1,"weight":1,"enabled":true}`)

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

	// 3. Call GET /v1/models on Gateway
	req, _ := http.NewRequest(http.MethodGet, gwURL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+keyData.Key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/models failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /v1/models status %d: %s", resp.StatusCode, string(b))
	}

	var modelList struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&modelList); err != nil {
		t.Fatalf("decode /v1/models: %v", err)
	}

	if modelList.Object != "list" {
		t.Fatalf("expected object='list', got %q", modelList.Object)
	}

	modelMap := make(map[string]bool)
	for _, m := range modelList.Data {
		if m.Object != "model" {
			t.Fatalf("expected item object='model', got %q", m.Object)
		}
		if m.OwnedBy == "" {
			t.Fatalf("expected item owned_by not empty")
		}
		modelMap[m.ID] = true
	}

	if !modelMap["claude-3-7-sonnet"] || !modelMap["gpt-4o"] {
		t.Fatalf("expected claude-3-7-sonnet and gpt-4o in /v1/models, got: %+v", modelList.Data)
	}
	if modelMap["disabled-model"] {
		t.Fatalf("disabled-model should not appear in routable models list")
	}
}

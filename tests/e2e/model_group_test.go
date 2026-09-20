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

func TestModelGroupEndToEndResolutionAndGatewayRouting(t *testing.T) {
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-group","choices":[{"message":{"role":"assistant","content":"group-response"}}]}`))
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

	// 1. Create provider, channel, models
	apiPost("/api/v1/providers", fmt.Sprintf(`{"id":"p-grp","name":"GrpProvider","adapter_type":"generic","protocol":"openai-chat","base_url_template":%q,"enabled":true}`, mockUpstream.URL))
	apiPost("/api/v1/channels", fmt.Sprintf(`{"id":"c-grp","provider_id":"p-grp","name":"GrpChan","base_url":%q,"enabled":true,"routing_enabled":true}`, mockUpstream.URL))
	apiPost("/api/v1/models", `{"id":"member-m1","display_name":"Member M1","enabled":true}`)
	apiPost("/api/v1/provider-models", `{"id":"pm-m1","provider_id":"p-grp","channel_id":"c-grp","model_id":"member-m1","upstream_model_name":"member-m1","protocol":"openai-chat","priority":1,"weight":1,"enabled":true}`)

	// 2. Create ModelGroup and add member
	apiPost("/api/v1/model-groups", `{"id":"group-auto","name":"auto-group","strategy":"priority","enabled":true}`)
	apiPost("/api/v1/model-groups/group-auto/members", `{"model_id":"member-m1","priority":1,"weight":1}`)

	// 3. Create route pointing to model_group
	apiPost("/api/v1/routes", `{"id":"route-grp","name":"AutoGroupRoute","protocol":"openai-chat","model_pattern":"auto-group","group_id":"group-auto","strategy":"priority","enabled":true}`)

	// 4. Issue local key
	keyResp, err := http.Post(apiURL+"/api/v1/keys", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /api/v1/keys: %v", err)
	}
	var keyData struct {
		Key string `json:"key"`
	}
	_ = json.NewDecoder(keyResp.Body).Decode(&keyData)
	keyResp.Body.Close()

	// 5. Call Gateway using group name as model
	gwReqBody := []byte(`{"model":"auto-group","messages":[{"role":"user","content":"hello group"}]}`)
	gwReq, _ := http.NewRequest(http.MethodPost, gwURL+"/v1/chat/completions", bytes.NewReader(gwReqBody))
	gwReq.Header.Set("Authorization", "Bearer "+keyData.Key)
	gwReq.Header.Set("Content-Type", "application/json")

	gwResp, err := http.DefaultClient.Do(gwReq)
	if err != nil {
		t.Fatalf("Gateway request failed: %v", err)
	}
	defer gwResp.Body.Close()

	gwRespBody, _ := io.ReadAll(gwResp.Body)
	if gwResp.StatusCode != http.StatusOK {
		t.Fatalf("Gateway with model group returned %d: %s", gwResp.StatusCode, string(gwRespBody))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(gwRespBody, &chatResp); err != nil || len(chatResp.Choices) == 0 {
		t.Fatalf("parse chat response failed: %s", string(gwRespBody))
	}
	if chatResp.Choices[0].Message.Content != "group-response" {
		t.Fatalf("expected 'group-response', got %q", chatResp.Choices[0].Message.Content)
	}
}

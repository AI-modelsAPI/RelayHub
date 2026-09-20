package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/router"
)

func TestCrossProtocolAnthropicClientToOpenAIUpstream(t *testing.T) {
	var upstreamReceivedPath string
	var upstreamReceivedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReceivedPath = r.URL.Path
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &upstreamReceivedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Upstream OpenAI returns OpenAI chat completion
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"model": "gpt-4o",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "Hello from OpenAI upstream!"
				},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 8,
				"total_tokens": 18
			}
		}`))
	}))
	defer srv.Close()

	rRes := router.Resolver{
		Models: map[string]domain.Model{
			"claude-opus": {ID: "claude-opus", Enabled: true},
		},
		Channels: map[string]domain.Channel{
			"chan-openai": {
				ID:             "chan-openai",
				ProviderID:     "prov-openai",
				BaseURL:        srv.URL,
				Enabled:        true,
				RoutingEnabled: true,
			},
		},
		ProviderModels: []domain.ProviderModel{
			{
				ID:                "pm-cross",
				ProviderID:        "prov-openai",
				ChannelID:         "chan-openai",
				ModelID:           "claude-opus",
				UpstreamModelName: "gpt-4o",
				Protocol:          "openai-chat",
				Enabled:           true,
			},
		},
	}

	gw := New(Config{
		Resolver: rRes,
		Upstream: HTTPUpstream{Client: srv.Client()},
	})

	// Anthropic client calls /v1/messages
	anthropicReqBody := `{
		"model": "claude-opus",
		"max_tokens": 100,
		"system": "You are helpful.",
		"messages": [
			{"role": "user", "content": "Hi there"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(anthropicReqBody))
	rec := httptest.NewRecorder()

	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	// 1. Upstream should receive /v1/chat/completions (NOT /v1/messages!)
	if upstreamReceivedPath != "/v1/chat/completions" {
		t.Fatalf("expected upstream path /v1/chat/completions, got %s", upstreamReceivedPath)
	}

	// 2. Upstream body should be OpenAI format (system in messages, model gpt-4o)
	if upstreamReceivedBody["model"] != "gpt-4o" {
		t.Fatalf("expected model gpt-4o, got %v", upstreamReceivedBody["model"])
	}

	// 3. Client should receive Anthropic format (type: message, content: [...], stop_reason: end_turn)
	var clientResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &clientResp); err != nil {
		t.Fatalf("failed to unmarshal client response: %v", err)
	}

	if clientResp["type"] != "message" {
		t.Fatalf("expected client response type 'message', got %v", clientResp["type"])
	}
	content, ok := clientResp["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected non-empty content array, got %v", clientResp["content"])
	}
	block := content[0].(map[string]any)
	if block["text"] != "Hello from OpenAI upstream!" {
		t.Fatalf("expected text 'Hello from OpenAI upstream!', got %v", block["text"])
	}
	if clientResp["stop_reason"] != "end_turn" {
		t.Fatalf("expected stop_reason 'end_turn', got %v", clientResp["stop_reason"])
	}
}

func TestCrossProtocolOpenAIClientToAnthropicUpstream(t *testing.T) {
	var upstreamReceivedPath string
	var upstreamReceivedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReceivedPath = r.URL.Path
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &upstreamReceivedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Upstream Anthropic returns Anthropic message
		_, _ = w.Write([]byte(`{
			"id": "msg-test",
			"type": "message",
			"role": "assistant",
			"model": "claude-3-5-sonnet-20241022",
			"content": [{
				"type": "text",
				"text": "Hello from Anthropic upstream!"
			}],
			"stop_reason": "end_turn",
			"usage": {
				"input_tokens": 15,
				"output_tokens": 12
			}
		}`))
	}))
	defer srv.Close()

	rRes := router.Resolver{
		Models: map[string]domain.Model{
			"gpt-mock": {ID: "gpt-mock", Enabled: true},
		},
		Channels: map[string]domain.Channel{
			"chan-anthropic": {
				ID:             "chan-anthropic",
				ProviderID:     "prov-anthropic",
				BaseURL:        srv.URL,
				Enabled:        true,
				RoutingEnabled: true,
			},
		},
		ProviderModels: []domain.ProviderModel{
			{
				ID:                "pm-anthropic",
				ProviderID:        "prov-anthropic",
				ChannelID:         "chan-anthropic",
				ModelID:           "gpt-mock",
				UpstreamModelName: "claude-3-5-sonnet-20241022",
				Protocol:          "anthropic-messages",
				Enabled:           true,
			},
		},
	}

	gw := New(Config{
		Resolver: rRes,
		Upstream: HTTPUpstream{Client: srv.Client()},
	})

	// OpenAI client calls /v1/chat/completions
	openAIReqBody := `{
		"model": "gpt-mock",
		"max_tokens": 100,
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hi there"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(openAIReqBody))
	rec := httptest.NewRecorder()

	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	// 1. Upstream should receive /v1/messages (NOT /v1/chat/completions!)
	if upstreamReceivedPath != "/v1/messages" {
		t.Fatalf("expected upstream path /v1/messages, got %s", upstreamReceivedPath)
	}

	// 2. Upstream body should be Anthropic format (system at top-level, max_tokens, messages without system)
	if upstreamReceivedBody["system"] != "You are helpful." {
		t.Fatalf("expected system 'You are helpful.', got %v", upstreamReceivedBody["system"])
	}
	if upstreamReceivedBody["model"] != "claude-3-5-sonnet-20241022" {
		t.Fatalf("expected model claude-3-5-sonnet-20241022, got %v", upstreamReceivedBody["model"])
	}

	// 3. Client should receive OpenAI format (object: chat.completion, choices: [...], finish_reason: stop)
	var clientResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &clientResp); err != nil {
		t.Fatalf("failed to unmarshal client response: %v", err)
	}

	choices, ok := clientResp["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("expected non-empty choices array, got %v", clientResp["choices"])
	}
	choice0 := choices[0].(map[string]any)
	msg := choice0["message"].(map[string]any)
	if msg["content"] != "Hello from Anthropic upstream!" {
		t.Fatalf("expected content 'Hello from Anthropic upstream!', got %v", msg["content"])
	}
	if choice0["finish_reason"] != "stop" {
		t.Fatalf("expected finish_reason 'stop', got %v", choice0["finish_reason"])
	}
}

func TestPassthroughSameProtocol(t *testing.T) {
	decision := router.Decision{
		Model: domain.Model{ID: "m1"},
		ProviderModel: domain.ProviderModel{
			UpstreamModelName: "up-m1",
			Protocol:          "openai-chat",
		},
	}
	raw := []byte(`{"model":"m1","messages":[{"role":"user","content":"hello"}],"custom_param":123}`)
	transformed, err := transformRequest("openai-chat", "/v1/chat/completions", raw, decision)
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	_ = json.Unmarshal(transformed, &res)
	if res["model"] != "up-m1" {
		t.Fatalf("expected up-m1, got %v", res["model"])
	}
	if res["custom_param"] != float64(123) {
		t.Fatalf("expected custom_param preserved, got %v", res["custom_param"])
	}
}

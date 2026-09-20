package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/router"
)

func TestTransformAnthropicToOpenAIRequest(t *testing.T) {
	anthropicReq := `{
		"model": "claude-3-opus",
		"max_tokens": 1024,
		"system": "You are a helpful assistant.",
		"messages": [
			{"role": "user", "content": "Hello world"}
		],
		"temperature": 0.7
	}`

	decision := router.Decision{
		Model: domain.Model{ID: "claude-3-opus"},
		ProviderModel: domain.ProviderModel{
			UpstreamModelName: "gpt-4o",
			Protocol:          "openai-chat",
		},
		Channel: domain.Channel{
			BaseURL: "https://api.openai.com",
		},
	}

	transformed, err := transformRequest("anthropic-messages", "/v1/messages", []byte(anthropicReq), decision)
	if err != nil {
		t.Fatalf("transformRequest failed: %v", err)
	}

	var openAIReq map[string]any
	if err := json.Unmarshal(transformed, &openAIReq); err != nil {
		t.Fatalf("unmarshal transformed failed: %v", err)
	}

	if openAIReq["model"] != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %v", openAIReq["model"])
	}
	if openAIReq["max_tokens"] != float64(1024) {
		t.Errorf("expected max_tokens 1024, got %v", openAIReq["max_tokens"])
	}
	// System message should be prepended to messages
	msgs, ok := openAIReq["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %v", openAIReq["messages"])
	}
	sysMsg := msgs[0].(map[string]any)
	if sysMsg["role"] != "system" || sysMsg["content"] != "You are a helpful assistant." {
		t.Errorf("unexpected system msg: %v", sysMsg)
	}
	userMsg := msgs[1].(map[string]any)
	if userMsg["role"] != "user" || userMsg["content"] != "Hello world" {
		t.Errorf("unexpected user msg: %v", userMsg)
	}
}

func TestTransformOpenAIToAnthropicResponse(t *testing.T) {
	openAIResp := `{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1677652288,
		"model": "gpt-4o",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "Hello there! How can I help you today?"
			},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 9,
			"completion_tokens": 12,
			"total_tokens": 21
		}
	}`

	decision := router.Decision{
		Model: domain.Model{ID: "claude-3-opus"},
		ProviderModel: domain.ProviderModel{
			UpstreamModelName: "gpt-4o",
			Protocol:          "openai-chat",
		},
	}

	transformed, err := transformResponse("anthropic-messages", []byte(openAIResp), decision, time.Now())
	if err != nil {
		t.Fatalf("transformResponse failed: %v", err)
	}

	var anthropicResp map[string]any
	if err := json.Unmarshal(transformed, &anthropicResp); err != nil {
		t.Fatalf("unmarshal transformed failed: %v", err)
	}

	if anthropicResp["type"] != "message" {
		t.Errorf("expected type 'message', got %v", anthropicResp["type"])
	}
	if anthropicResp["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got %v", anthropicResp["role"])
	}
	if anthropicResp["stop_reason"] != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %v", anthropicResp["stop_reason"])
	}
	content, ok := anthropicResp["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected content array, got %v", anthropicResp["content"])
	}
	block := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "Hello there! How can I help you today?" {
		t.Errorf("unexpected content block: %v", block)
	}
	usage, ok := anthropicResp["usage"].(map[string]any)
	if !ok || usage["input_tokens"] != float64(9) || usage["output_tokens"] != float64(12) {
		t.Errorf("unexpected usage: %v", anthropicResp["usage"])
	}
}

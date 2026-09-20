package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"relayhub/internal/affinity"
	"relayhub/internal/domain"
	"relayhub/internal/router"
)

func TestCrossProtocolStreamingAnthropicClientOpenAIUpstream(t *testing.T) {
	up := &fakeUpstream{responses: []Response{response(200, strings.Join([]string{
		`data: {"id":"chatcmpl-x","choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-x","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))}}
	rRes := resolverForTest()
	h := New(Config{Resolver: rRes, Upstream: up})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"logical","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	if !strings.Contains(body, "event: content_block_delta") || !strings.Contains(body, "Hello") {
		t.Fatalf("expected anthropic SSE, got %s", body)
	}
	if strings.Contains(body, "[DONE]") {
		t.Fatalf("anthropic client should not receive OpenAI [DONE]: %s", body)
	}
}

func TestCapabilityFlagsExcludeModelsWithoutTools(t *testing.T) {
	rRes := resolverForTest()
	m := rRes.Models["logical"]
	m.ToolCallSupport = false
	rRes.Models["logical"] = m
	up := &fakeUpstream{responses: []Response{response(200, `{"choices":[{"message":{"content":"x"}}]}`)}}
	h := New(Config{Resolver: rRes, Upstream: up})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[],"tools":[{"type":"function","function":{"name":"x"}}]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == 200 {
		t.Fatalf("expected routing failure when tools required, body=%s", w.Body.String())
	}
}

func TestUnauthorizedDisablesKey(t *testing.T) {
	var disabled atomic.Value
	up := &fakeUpstream{responses: []Response{
		{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"error":"bad"}`)), CredentialKeyID: "k-bad"},
		response(200, `{"choices":[{"message":{"content":"ok"}}]}`),
	}}
	h := New(Config{
		Resolver:    resolverForTest(),
		Upstream:    up,
		DisableKey:  func(_ context.Context, id string) { disabled.Store(id) },
		MaxAttempts: 2,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if disabled.Load() != "k-bad" {
		t.Fatalf("key not disabled: %v", disabled.Load())
	}
}

func TestSessionStickinessPinsChannel(t *testing.T) {
	sticky := affinity.New(0)
	sticky.Remember("sess-1", "c", "")
	rRes := resolverForTest()
	rRes.Sticky = sticky
	up := &fakeUpstream{responses: []Response{response(200, `{"choices":[{"message":{"content":"ok"}}]}`)}}
	h := New(Config{Resolver: rRes, Upstream: up})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[],"metadata":{"user_id":"sess-1"}}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
}

func TestRecordParsesCacheTokens(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Request-ID", "r1")
	body := []byte(`{"model":"up-m","usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":8}},"choices":[{"finish_reason":"stop"}]}`)
	rec := recordFor(req, "openai-chat", router.Decision{
		Model:         domain.Model{ID: "logical"},
		ProviderModel: domain.ProviderModel{ProviderID: "p", UpstreamModelName: "x"},
		Channel:       domain.Channel{ID: "c"},
	}, body, 200, time.Now())
	if rec.CacheReadTokens != 8 || rec.FinishReason != "stop" || rec.UpstreamModel != "up-m" {
		t.Fatalf("%+v", rec)
	}
}

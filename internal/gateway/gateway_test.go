package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/router"
	"relayhub/internal/usage"
)

type fakeUpstream struct {
	responses []Response
	requests  []Request
}

func (f *fakeUpstream) Do(_ context.Context, req Request) (Response, error) {
	f.requests = append(f.requests, req)
	return f.responses[len(f.requests)-1], nil
}
func response(status int, body string) Response {
	return Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}
func resolverForTest() router.Resolver {
	return router.Resolver{Models: map[string]domain.Model{"logical": {ID: "logical", Enabled: true}}, Channels: map[string]domain.Channel{"c": {ID: "c", ProviderID: "p", BaseURL: "http://upstream", Enabled: true, RoutingEnabled: true}}, ProviderModels: []domain.ProviderModel{{ID: "pm", ProviderID: "p", ModelID: "logical", ChannelID: "c", UpstreamModelName: "upstream", Protocol: "openai-chat", Enabled: true}}}
}

func TestGatewayInjectsBearerAndAnthropicCredential(t *testing.T) {
	var receivedAuth, receivedKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	upstream := HTTPUpstream{
		Client: srv.Client(),
		Credential: func(ctx context.Context, d router.Decision) (string, error) {
			if d.Channel.CredentialRef == "cred-secret" {
				return "my-secret-key", nil
			}
			return "", nil
		},
	}

	// 1. Test OpenAI protocol
	reqOpenAI := Request{
		Protocol: "openai-chat",
		Path:     srv.URL,
		Decision: router.Decision{
			Channel: domain.Channel{BaseURL: srv.URL, CredentialRef: "cred-secret"},
		},
		Body: []byte(`{}`),
	}
	resp, err := upstream.Do(context.Background(), reqOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if receivedAuth != "Bearer my-secret-key" || receivedKey != "" {
		t.Fatalf("OpenAI upstream auth mismatch: got Authorization=%q, x-api-key=%q", receivedAuth, receivedKey)
	}

	// 2. Test Anthropic protocol
	receivedAuth = ""
	receivedKey = ""
	reqAnthropic := Request{
		Protocol: "anthropic-messages",
		Path:     srv.URL,
		Decision: router.Decision{
			Channel: domain.Channel{BaseURL: srv.URL, CredentialRef: "cred-secret"},
		},
		Body: []byte(`{}`),
	}
	resp, err = upstream.Do(context.Background(), reqAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if receivedKey != "my-secret-key" || receivedAuth != "" {
		t.Fatalf("Anthropic upstream auth mismatch: got Authorization=%q, x-api-key=%q", receivedAuth, receivedKey)
	}

	// 3. Test Empty CredentialRef -> no error and no credential header
	receivedAuth = ""
	receivedKey = ""
	reqNoCred := Request{
		Protocol: "openai-chat",
		Path:     srv.URL,
		Decision: router.Decision{
			Channel: domain.Channel{BaseURL: srv.URL, CredentialRef: ""},
		},
		Body: []byte(`{}`),
	}
	resp, err = upstream.Do(context.Background(), reqNoCred)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if receivedAuth != "" || receivedKey != "" {
		t.Fatalf("expected empty auth headers for empty ref, got Authorization=%q, x-api-key=%q", receivedAuth, receivedKey)
	}
}

// TestUpstreamDoesNotDoubleVersionSegment reproduces the UI-created-channel bug:
// base_url ends in /v1 (the UI auto-appends it) and the client path is
// /v1/chat/completions. Joining naively produced /v1/v1/chat/completions -> 404.
// The upstream request must land on exactly one /v1 segment.
func TestUpstreamDoesNotDoubleVersionSegment(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	up := HTTPUpstream{Client: srv.Client()}
	// Channel base_url already ends in /v1 (as the UI stores it).
	req := Request{
		Protocol: "openai-chat",
		Path:     "/v1/chat/completions",
		Decision: router.Decision{Channel: domain.Channel{BaseURL: srv.URL + "/v1"}},
		Body:     []byte(`{}`),
	}
	resp, err := up.Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path doubled version segment: got %q, want /v1/chat/completions", gotPath)
	}
}

func TestOpenAINonStreamingMapsModelAndRecordsUsage(t *testing.T) {
	up := &fakeUpstream{responses: []Response{response(200, `{"id":"x","model":"upstream","usage":{"prompt_tokens":2,"completion_tokens":3}}`)}}
	recorder := &usage.MemoryRecorder{}
	h := New(Config{Resolver: resolverForTest(), Upstream: up, Recorder: recorder})
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if string(up.requests[0].Body) == `{"model":"logical","messages":[]}` {
		t.Fatal("model was not mapped")
	}
	got := recorder.Records()
	if len(got) != 1 || got[0].InputTokens != 2 || got[0].OutputTokens != 3 {
		t.Fatalf("records=%+v", got)
	}
}
func TestGatewaySSEFlushesAndDoesNotRetryAfterStream(t *testing.T) {
	up := &fakeUpstream{responses: []Response{response(200, "data: {\"id\":\"x\"}\n\ndata: [DONE]\n\n")}}
	h := New(Config{Resolver: resolverForTest(), Upstream: up})
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","stream":true}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "[DONE]") || len(up.requests) != 1 {
		t.Fatalf("status=%d calls=%d body=%s", w.Code, len(up.requests), w.Body.String())
	}
}
func TestAnthropicEndpointUsesAnthropicEnvelope(t *testing.T) {
	up := &fakeUpstream{responses: []Response{response(200, `{"type":"message","model":"upstream"}`)}}
	routerConfig := resolverForTest()
	routerConfig.ProviderModels[0].Protocol = "anthropic"
	h := New(Config{Resolver: routerConfig, Upstream: up})
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"logical","messages":[]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"model":"logical"`) {
		t.Fatalf("body=%s", w.Body.String())
	}
}

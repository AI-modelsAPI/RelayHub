package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/router"
)

// probeScriptedUpstream answers probe requests in order and records them.
// (scriptedUpstream in health_signal_test.go is a different helper: delay
// steps, no request recording.)
type probeScriptedUpstream struct {
	replies  []Response
	errs     []error
	requests []Request
}

func (s *probeScriptedUpstream) Do(_ context.Context, req Request) (Response, error) {
	s.requests = append(s.requests, req)
	i := len(s.requests) - 1
	if i < len(s.errs) && s.errs[i] != nil {
		return Response{}, s.errs[i]
	}
	if i >= len(s.replies) {
		return Response{}, errors.New("unexpected extra probe request")
	}
	return s.replies[i], nil
}

func probeDecision(protocol, upstreamModel string, m domain.Model) router.Decision {
	pm := domain.ProviderModel{ID: "pm", ProviderID: "p", ChannelID: "c", ModelID: m.ID, UpstreamModelName: upstreamModel, Protocol: protocol, Enabled: true}
	ch := domain.Channel{ID: "c", ProviderID: "p", Name: "relay", BaseURL: "http://relay.invalid", Enabled: true,
		CustomHeaders: map[string]string{"User-Agent": "claude-cli/2.0 (external, cli)"}}
	return router.Decision{Model: m, ProviderModel: pm, Channel: ch, Transform: pm, Excluded: map[string]string{}}
}

const probeTestCode = "apple river stone cloud"

func fixedCode() string { return probeTestCode }

func decodeProbeBody(t *testing.T, req Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("probe body is not JSON: %v %s", err, req.Body)
	}
	return body
}

func firstMessageText(t *testing.T, body map[string]any) string {
	t.Helper()
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected one user message: %v", body["messages"])
	}
	msg, _ := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Fatalf("message role: %v", msg)
	}
	text, _ := msg["content"].(string)
	return text
}

func TestProbeOpenAIChatCanaryAndTools(t *testing.T) {
	up := &probeScriptedUpstream{replies: []Response{
		response(200, `{"id":"x","model":"gpt-4o-2024-08-06","choices":[{"index":0,"message":{"role":"assistant","content":"Apple, river, stone, cloud."},"finish_reason":"stop"}],"usage":{"prompt_tokens":31,"completion_tokens":6}}`),
		response(200, `{"model":"gpt-4o","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"t1","type":"function","function":{"name":"record_code","arguments":"{\"code\":\"apple river stone cloud\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":88}}`),
	}}
	p := Prober{Upstream: up, Code: fixedCode}
	o := p.Probe(context.Background(), probeDecision("openai-chat", "gpt-4o", domain.Model{ID: "gpt-4o", ToolCallSupport: true}))

	if o.ChannelID != "c" || o.ModelID != "gpt-4o" || o.UpstreamModel != "gpt-4o" || o.Protocol != "openai-chat" || o.At.IsZero() {
		t.Fatalf("outcome identity: %+v", o)
	}
	if o.CanaryStatus != 200 || o.CanaryError != "" || !o.CanaryEchoed || o.CanaryTruncated {
		t.Fatalf("canary: %+v", o)
	}
	if o.ReportedModel != "gpt-4o-2024-08-06" || o.PromptTokens != 31 {
		t.Fatalf("model field / fingerprint: %+v", o)
	}
	if !o.ToolsChecked || o.ToolsStatus != 200 || !o.ToolsCalled || o.ToolsError != "" {
		t.Fatalf("tools: %+v", o)
	}

	if len(up.requests) != 2 {
		t.Fatalf("expected canary + tool request, got %d", len(up.requests))
	}
	canary := up.requests[0]
	if canary.Path != "/v1/chat/completions" || canary.Protocol != "openai-chat" || canary.Stream || canary.Decision.Channel.ID != "c" {
		t.Fatalf("canary envelope: %+v", canary)
	}
	if canary.Headers.Get("Content-Type") != "application/json" || canary.Headers.Get("User-Agent") != "claude-cli/2.0 (external, cli)" {
		t.Fatalf("probe must look like channel traffic (identity headers): %v", canary.Headers)
	}
	body := decodeProbeBody(t, canary)
	if body["model"] != "gpt-4o" || body["max_tokens"] == nil || body["tools"] != nil {
		t.Fatalf("canary body: %v", body)
	}
	if !strings.Contains(firstMessageText(t, body), probeTestCode) {
		t.Fatalf("canary prompt must carry the code: %v", body)
	}

	tools := decodeProbeBody(t, up.requests[1])
	choice, _ := tools["tool_choice"].(map[string]any)
	fn, _ := choice["function"].(map[string]any)
	if choice["type"] != "function" || fn["name"] != "record_code" {
		t.Fatalf("tool call must be forced: %v", tools["tool_choice"])
	}
	list, _ := tools["tools"].([]any)
	if len(list) != 1 {
		t.Fatalf("tools: %v", tools["tools"])
	}
	def, _ := list[0].(map[string]any)
	fdef, _ := def["function"].(map[string]any)
	if def["type"] != "function" || fdef["name"] != "record_code" || fdef["parameters"] == nil {
		t.Fatalf("tool definition: %v", def)
	}
}

func TestProbeAnthropicMessages(t *testing.T) {
	up := &probeScriptedUpstream{replies: []Response{
		response(200, `{"type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"text","text":"apple river stone cloud"}],"stop_reason":"end_turn","usage":{"input_tokens":18,"cache_creation_input_tokens":0,"cache_read_input_tokens":2,"output_tokens":7}}`),
		response(200, `{"type":"message","model":"claude-sonnet-4-5-20250929","content":[{"type":"tool_use","id":"toolu_1","name":"record_code","input":{"code":"apple river stone cloud"}}],"stop_reason":"tool_use","usage":{"input_tokens":402}}`),
	}}
	p := Prober{Upstream: up, Code: fixedCode}
	o := p.Probe(context.Background(), probeDecision("anthropic-messages", "claude-sonnet-4-5", domain.Model{ID: "sonnet", ToolCallSupport: true}))
	if !o.CanaryEchoed || o.ReportedModel != "claude-sonnet-4-5-20250929" || o.PromptTokens != 20 || !o.ToolsCalled {
		t.Fatalf("anthropic outcome: %+v", o)
	}
	if len(up.requests) != 2 {
		t.Fatalf("requests: %d", len(up.requests))
	}
	canary := up.requests[0]
	if canary.Path != "/v1/messages" || canary.Protocol != "anthropic-messages" || canary.Headers.Get("anthropic-version") == "" {
		t.Fatalf("anthropic envelope: %+v", canary)
	}
	body := decodeProbeBody(t, canary)
	if body["model"] != "claude-sonnet-4-5" || body["max_tokens"] == nil || !strings.Contains(firstMessageText(t, body), probeTestCode) {
		t.Fatalf("anthropic canary body: %v", body)
	}
	tools := decodeProbeBody(t, up.requests[1])
	choice, _ := tools["tool_choice"].(map[string]any)
	if choice["type"] != "tool" || choice["name"] != "record_code" {
		t.Fatalf("anthropic tool_choice: %v", tools["tool_choice"])
	}
	list, _ := tools["tools"].([]any)
	def, _ := list[0].(map[string]any)
	if def["name"] != "record_code" || def["input_schema"] == nil {
		t.Fatalf("anthropic tool definition: %v", tools["tools"])
	}
}

func TestProbeRecordsCannedRepliesAndPlainTextTools(t *testing.T) {
	up := &probeScriptedUpstream{replies: []Response{
		response(200, `{"model":"gpt-4o","choices":[{"message":{"content":"Hello! How can I help you today?"},"finish_reason":"stop"}],"usage":{"prompt_tokens":31}}`),
		response(200, `{"model":"gpt-4o","choices":[{"message":{"content":"Sure, the code is apple river stone cloud."},"finish_reason":"stop"}]}`),
	}}
	o := Prober{Upstream: up, Code: fixedCode}.Probe(context.Background(), probeDecision("openai-chat", "gpt-4o", domain.Model{ID: "gpt-4o", ToolCallSupport: true}))
	if o.CanaryStatus != 200 || o.CanaryEchoed || o.CanaryTruncated || !strings.Contains(o.CanaryText, "How can I help") {
		t.Fatalf("canned reply: %+v", o)
	}
	if !o.ToolsChecked || o.ToolsStatus != 200 || o.ToolsCalled || o.ToolsError != "" {
		t.Fatalf("plain text instead of a tool call: %+v", o)
	}
}

func TestProbeUpstreamErrorsAreReportedNotJudged(t *testing.T) {
	// A rejected canary stops the probe: no tool request is spent.
	up := &probeScriptedUpstream{replies: []Response{response(401, `{"error":{"message":"invalid api key"}}`)}}
	o := Prober{Upstream: up, Code: fixedCode}.Probe(context.Background(), probeDecision("openai-chat", "gpt-4o", domain.Model{ID: "gpt-4o", ToolCallSupport: true}))
	if o.CanaryStatus != 401 || !strings.Contains(o.CanaryError, "401") || !strings.Contains(o.CanaryError, "invalid api key") || o.ToolsChecked || len(up.requests) != 1 {
		t.Fatalf("401 canary: %+v (%d requests)", o, len(up.requests))
	}

	up = &probeScriptedUpstream{errs: []error{errors.New("dial tcp: i/o timeout")}}
	o = Prober{Upstream: up, Code: fixedCode}.Probe(context.Background(), probeDecision("anthropic-messages", "claude-sonnet-4-5", domain.Model{ID: "sonnet"}))
	if o.CanaryStatus != 0 || !strings.Contains(o.CanaryError, "i/o timeout") {
		t.Fatalf("transport error: %+v", o)
	}

	up = &probeScriptedUpstream{replies: []Response{response(200, `<html><body>Just a moment...</body></html>`)}}
	o = Prober{Upstream: up, Code: fixedCode}.Probe(context.Background(), probeDecision("openai-chat", "gpt-4o", domain.Model{ID: "gpt-4o"}))
	if o.CanaryStatus != 200 || o.CanaryError == "" || o.CanaryEchoed {
		t.Fatalf("HTML under 200 is not an answer: %+v", o)
	}

	up = &probeScriptedUpstream{replies: []Response{response(200, `{"error":{"type":"overloaded_error","message":"Overloaded"}}`)}}
	o = Prober{Upstream: up, Code: fixedCode}.Probe(context.Background(), probeDecision("anthropic-messages", "claude-sonnet-4-5", domain.Model{ID: "sonnet"}))
	if o.CanaryError == "" || !strings.Contains(o.CanaryError, "Overloaded") {
		t.Fatalf("error object under 200 is not an answer: %+v", o)
	}
}

func TestProbeReasoningModelsGetATokenBudget(t *testing.T) {
	up := &probeScriptedUpstream{replies: []Response{
		response(200, `{"model":"o3","choices":[{"message":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":30}}`),
	}}
	o := Prober{Upstream: up, Code: fixedCode}.Probe(context.Background(), probeDecision("openai-chat", "o3", domain.Model{ID: "o3", ReasoningSupport: true}))
	body := decodeProbeBody(t, up.requests[0])
	if body["max_tokens"] != nil || body["max_completion_tokens"] == nil {
		t.Fatalf("OpenAI reasoning models reject max_tokens: %v", body)
	}
	if !o.CanaryTruncated || o.CanaryEchoed {
		t.Fatalf("finish_reason=length must be reported as truncated: %+v", o)
	}
	if o.ToolsChecked || len(up.requests) != 1 {
		t.Fatalf("tools must only be probed for models that declare them: %+v", o)
	}
}

func TestProbeProtocolSelection(t *testing.T) {
	// No explicit protocol: Claude models are probed over Messages.
	up := &probeScriptedUpstream{replies: []Response{response(200, `{"model":"claude-opus-4-1","content":[{"type":"text","text":"apple river stone cloud"}],"stop_reason":"end_turn","usage":{"input_tokens":20}}`)}}
	o := Prober{Upstream: up, Code: fixedCode}.Probe(context.Background(), probeDecision("", "claude-opus-4-1", domain.Model{ID: "opus"}))
	if o.Protocol != "anthropic-messages" || up.requests[0].Path != "/v1/messages" || !o.CanaryEchoed {
		t.Fatalf("implicit protocol: %+v", o)
	}
	// Protocols the prober cannot speak are reported without a request.
	up = &probeScriptedUpstream{}
	o = Prober{Upstream: up, Code: fixedCode}.Probe(context.Background(), probeDecision("gemini", "gemini-2.5-pro", domain.Model{ID: "gemini-2.5-pro"}))
	if len(up.requests) != 0 || o.CanaryStatus != 0 || !strings.Contains(o.CanaryError, "gemini") {
		t.Fatalf("unsupported protocol: %+v", o)
	}
}

func TestProbeCodesAreRandomWords(t *testing.T) {
	known := map[string]bool{}
	for _, w := range probeWords {
		known[w] = true
	}
	if len(known) < 32 {
		t.Fatalf("word list too small for unpredictable codes: %d", len(known))
	}
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		code := probeCode()
		words := strings.Fields(code)
		if len(words) != 4 {
			t.Fatalf("code %q: want four words", code)
		}
		for _, w := range words {
			if !known[w] {
				t.Fatalf("code %q uses a word outside the list", code)
			}
		}
		seen[code] = true
	}
	if len(seen) < 2 {
		t.Fatal("probe codes do not vary")
	}
}

// The canary goes through the production HTTP upstream like client traffic:
// the channel credential and base URL are used, redirects are refused.
func TestProbeUsesTheProductionUpstream(t *testing.T) {
	var gotAuth, gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotUA = r.Header.Get("Authorization"), r.URL.Path, r.Header.Get("User-Agent")
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		msgs, _ := req["messages"].([]any)
		first, _ := msgs[0].(map[string]any)
		prompt, _ := first["content"].(string)
		code := prompt[strings.LastIndex(prompt, ":")+1:]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o","choices":[{"message":{"content":` + strconv.Quote(strings.TrimSpace(code)) + `},"finish_reason":"stop"}],"usage":{"prompt_tokens":31}}`))
	}))
	defer srv.Close()
	d := probeDecision("openai-chat", "gpt-4o", domain.Model{ID: "gpt-4o"})
	d.Channel.BaseURL = srv.URL + "/v1"
	up := HTTPUpstream{Client: srv.Client(), Credential: func(context.Context, router.Decision) (string, error) { return "sk-channel", nil }}
	o := Prober{Upstream: up}.Probe(context.Background(), d)
	if !o.CanaryEchoed || o.CanaryStatus != 200 {
		t.Fatalf("round trip through HTTPUpstream: %+v", o)
	}
	if gotAuth != "Bearer sk-channel" || gotPath != "/v1/chat/completions" || gotUA != "claude-cli/2.0 (external, cli)" {
		t.Fatalf("upstream saw auth=%q path=%q ua=%q", gotAuth, gotPath, gotUA)
	}
}

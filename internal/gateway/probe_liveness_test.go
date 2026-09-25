package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"relayhub/internal/domain"
)

// AUDIT §5 B5: a tripped channel comes back through one cheap synthetic
// request (max_tokens=1) instead of real client traffic.
func TestProbeLivenessSendsATinyRequest(t *testing.T) {
	up := &probeScriptedUpstream{replies: []Response{
		response(http.StatusOK, `{"model":"gpt-4o","choices":[{"index":0,"message":{"content":"P"},"finish_reason":"length"}],"usage":{"prompt_tokens":4}}`),
	}}
	res := Prober{Upstream: up}.ProbeLiveness(context.Background(), probeDecision("openai-chat", "gpt-4o", domain.Model{ID: "gpt-4o"}))
	if !res.OK || res.Status != http.StatusOK || res.Latency <= 0 || res.Error != "" {
		t.Fatalf("liveness result: %+v", res)
	}
	if len(up.requests) != 1 {
		t.Fatalf("expected exactly one request, got %d", len(up.requests))
	}
	req := up.requests[0]
	if req.Path != "/v1/chat/completions" || req.Protocol != "openai-chat" || req.Stream {
		t.Fatalf("request envelope: %+v", req)
	}
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("body: %v (%s)", err, req.Body)
	}
	if body["max_tokens"] != float64(1) {
		t.Fatalf("a liveness check must cap the answer at one token: %v", body)
	}
	if body["tools"] != nil {
		t.Fatalf("a liveness check must not spend a tool call: %v", body)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages: %v", body["messages"])
	}
}

func TestProbeLivenessAnthropicAndReasoning(t *testing.T) {
	up := &probeScriptedUpstream{replies: []Response{
		response(http.StatusOK, `{"type":"message","content":[{"type":"text","text":"P"}],"stop_reason":"max_tokens","usage":{"input_tokens":4,"output_tokens":1}}`),
	}}
	res := Prober{Upstream: up}.ProbeLiveness(context.Background(), probeDecision("anthropic-messages", "claude-sonnet-4-5", domain.Model{ID: "sonnet"}))
	if !res.OK || up.requests[0].Path != "/v1/messages" {
		t.Fatalf("anthropic liveness: %+v %+v", res, up.requests)
	}
	var body map[string]any
	_ = json.Unmarshal(up.requests[0].Body, &body)
	if body["max_tokens"] != float64(1) {
		t.Fatalf("anthropic liveness body: %v", body)
	}

	up = &probeScriptedUpstream{replies: []Response{
		response(http.StatusOK, `{"model":"o3","choices":[{"message":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":4}}`),
	}}
	res = Prober{Upstream: up}.ProbeLiveness(context.Background(), probeDecision("openai-chat", "o3", domain.Model{ID: "o3", ReasoningSupport: true}))
	if !res.OK {
		t.Fatalf("reasoning liveness: %+v", res)
	}
	body = map[string]any{}
	_ = json.Unmarshal(up.requests[0].Body, &body)
	if body["max_tokens"] != nil || body["max_completion_tokens"] == nil {
		t.Fatalf("reasoning models reject max_tokens: %v", body)
	}
}

func TestProbeLivenessFailuresAreReported(t *testing.T) {
	cases := []struct {
		name   string
		reply  Response
		err    error
		status int
		want   string
	}{
		{name: "overloaded", reply: response(529, `{"error":{"message":"Overloaded"}}`), status: 529, want: "Overloaded"},
		{name: "auth", reply: response(401, `{"error":{"message":"invalid api key"}}`), status: 401, want: "401"},
		{name: "html", reply: response(200, `<html>Just a moment...</html>`), status: 200, want: "not a JSON completion"},
		{name: "error object under 200", reply: response(200, `{"error":{"message":"balance not enough"}}`), status: 200, want: "balance not enough"},
		{name: "transport", err: errors.New("dial tcp: i/o timeout"), status: 0, want: "i/o timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := &probeScriptedUpstream{replies: []Response{tc.reply}, errs: []error{tc.err}}
			res := Prober{Upstream: up}.ProbeLiveness(context.Background(), probeDecision("openai-chat", "gpt-4o", domain.Model{ID: "gpt-4o"}))
			if res.OK || res.Status != tc.status || !strings.Contains(res.Error, tc.want) {
				t.Fatalf("liveness result: %+v (want %q)", res, tc.want)
			}
		})
	}

	// Protocols the prober cannot speak are refused without a request.
	up := &probeScriptedUpstream{}
	res := Prober{Upstream: up}.ProbeLiveness(context.Background(), probeDecision("gemini", "gemini-2.5-pro", domain.Model{ID: "gemini-2.5-pro"}))
	if res.OK || len(up.requests) != 0 || !strings.Contains(res.Error, "gemini") {
		t.Fatalf("unsupported protocol: %+v", res)
	}
}

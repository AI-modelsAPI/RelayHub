package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/health"
	"relayhub/internal/router"
	"relayhub/internal/usage"
)

// AUDIT 2026-09-24 §5 B4: writeSSE commits "200 OK" before reading a single
// event, so a relay answering a stream with 200 followed by an error event, an
// HTML page or nothing at all reached the client as a 200 stream and never
// failed over. These tests pin the pre-commit window that fixes it.

// channelUpstream answers attempt i with responses[i] and remembers which
// channel each attempt was sent to.
type channelUpstream struct {
	responses []Response
	channels  []string
}

func (u *channelUpstream) Do(_ context.Context, req Request) (Response, error) {
	u.channels = append(u.channels, req.Decision.Channel.ID)
	return u.responses[len(u.channels)-1], nil
}

func sseResponse(lines ...string) Response {
	hdr := http.Header{}
	hdr.Set("Content-Type", "text/event-stream")
	return Response{StatusCode: http.StatusOK, Header: hdr, Body: io.NopCloser(strings.NewReader(strings.Join(lines, "\n") + "\n"))}
}

// twoChannelResolver serves the logical model from channels a and b, both
// speaking the given upstream protocol.
func twoChannelResolver(protocol string) router.Resolver {
	return router.Resolver{
		Models: map[string]domain.Model{"logical": {ID: "logical", Enabled: true}},
		Channels: map[string]domain.Channel{
			"a": {ID: "a", ProviderID: "p", BaseURL: "http://a", Enabled: true, RoutingEnabled: true},
			"b": {ID: "b", ProviderID: "p", BaseURL: "http://b", Enabled: true, RoutingEnabled: true},
		},
		ProviderModels: []domain.ProviderModel{
			{ID: "pa", ProviderID: "p", ModelID: "logical", ChannelID: "a", UpstreamModelName: "up", Protocol: protocol, Enabled: true},
			{ID: "pb", ProviderID: "p", ModelID: "logical", ChannelID: "b", UpstreamModelName: "up", Protocol: protocol, Enabled: true},
		},
	}
}

const openAIStreamRequest = `{"model":"logical","stream":true,"messages":[{"role":"user","content":"hi"}]}`

const anthropicStreamRequest = `{"model":"logical","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`

var goodOpenAIStream = []string{
	`data: {"id":"c2","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
	``,
	`data: {"id":"c2","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
	``,
	`data: {"id":"c2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	``,
	`data: [DONE]`,
	``,
}

var goodAnthropicStream = []string{
	`event: message_start`,
	`data: {"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"up","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
	``,
	`event: content_block_start`,
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
	``,
	`event: content_block_delta`,
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi there"}}`,
	``,
	`event: content_block_stop`,
	`data: {"type":"content_block_stop","index":0}`,
	``,
	`event: message_delta`,
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
	``,
	`event: message_stop`,
	`data: {"type":"message_stop"}`,
	``,
}

// serveFailover sends one streaming request through a gateway whose first
// attempt gets first and whose second attempt gets goodOpenAIStream or
// goodAnthropicStream, and checks that the client only ever saw the second.
func serveFailover(t *testing.T, protocol string, first Response) {
	t.Helper()
	path, reqBody, good := "/v1/chat/completions", openAIStreamRequest, goodOpenAIStream
	if protocol == "anthropic" {
		path, reqBody, good = "/v1/messages", anthropicStreamRequest, goodAnthropicStream
	}
	up := &channelUpstream{responses: []Response{first, sseResponse(good...)}}
	reg := health.NewRegistry()
	h := New(Config{Resolver: twoChannelResolver(protocol), Upstream: up, Health: reg})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(reqBody)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(up.channels) != 2 || up.channels[0] == up.channels[1] {
		t.Fatalf("expected a failover to the other channel, attempts went to %v", up.channels)
	}
	if want := strings.Join(good, "\n") + "\n"; w.Body.String() != want {
		t.Fatalf("client must receive exactly the healthy channel's stream\n got: %q\nwant: %q", w.Body.String(), want)
	}
	if s := reg.Get(up.channels[0]); s.WindowTotal != 1 || s.WindowSuccess != 0 {
		t.Fatalf("the failed channel must be penalised: %+v", s)
	}
	if s := reg.Get(up.channels[1]); s.WindowTotal != 1 || s.WindowSuccess != 1 {
		t.Fatalf("the healthy channel must record one success: %+v", s)
	}
}

func TestStreamErrorEventBeforeFirstDeltaFailsOver(t *testing.T) {
	t.Run("openai error chunk", func(t *testing.T) {
		serveFailover(t, "openai-chat", sseResponse(
			`data: {"error":{"message":"upstream overloaded, retry later","type":"server_error"}}`,
			``,
			`data: [DONE]`,
		))
	})
	t.Run("openai role chunk then error", func(t *testing.T) {
		serveFailover(t, "openai-chat", sseResponse(
			`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
			``,
			`data: {"error":{"message":"You exceeded your current quota","type":"insufficient_quota"}}`,
		))
	})
	t.Run("anthropic message_start and ping then error", func(t *testing.T) {
		serveFailover(t, "anthropic", sseResponse(
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"up","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
			``,
			`event: ping`,
			`data: {"type":"ping"}`,
			``,
			`event: error`,
			`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		))
	})
	t.Run("empty stream", func(t *testing.T) {
		serveFailover(t, "openai-chat", sseResponse(`: keep-alive`, ``, `data: [DONE]`))
	})
	t.Run("malformed first event", func(t *testing.T) {
		serveFailover(t, "openai-chat", sseResponse(`data: <b>502 Bad Gateway</b>`))
	})
}

func TestStreamHTMLBeforeFirstDeltaFailsOver(t *testing.T) {
	t.Run("html body behind an event-stream header", func(t *testing.T) {
		serveFailover(t, "openai-chat", sseResponse(`<!DOCTYPE html>`, `<html><title>Just a moment...</title></html>`))
	})
	t.Run("text/html header", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("Content-Type", "text/html; charset=utf-8")
		serveFailover(t, "openai-chat", Response{StatusCode: http.StatusOK, Header: hdr, Body: io.NopCloser(strings.NewReader("<html>insufficient balance</html>"))})
	})
}

// The non-stream path already rejected text/html (F9) but only after the
// attempt loop, so it could not fail over either.
func TestNonStreamHTMLFailsOver(t *testing.T) {
	hdr := http.Header{}
	hdr.Set("Content-Type", "text/html")
	html := Response{StatusCode: http.StatusOK, Header: hdr, Body: io.NopCloser(strings.NewReader("<html>Just a moment...</html>"))}
	up := &channelUpstream{responses: []Response{html, response(http.StatusOK, `{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)}}
	reg := health.NewRegistry()
	h := New(Config{Resolver: twoChannelResolver("openai-chat"), Upstream: up, Health: reg})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[]}`)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok"`) || len(up.channels) != 2 {
		t.Fatalf("status=%d attempts=%v body=%s", w.Code, up.channels, w.Body.String())
	}
	if s := reg.Get(up.channels[0]); s.WindowTotal != 1 || s.WindowSuccess != 0 {
		t.Fatalf("the HTML channel must record a failure and no success: %+v", s)
	}
}

// With no channel left the client now gets an HTTP error carrying the
// upstream's error instead of "200 OK" wrapping it.
func TestStreamErrorEventWithoutFailoverBecomesHTTPError(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		protocol string
		stream   []string
		status   int
		body     string
		class    string
	}{
		{
			name:     "openai rate limit",
			path:     "/v1/chat/completions",
			protocol: "openai-chat",
			stream:   []string{`data: {"error":{"message":"Rate limit reached","type":"rate_limit_error"}}`},
			status:   http.StatusTooManyRequests,
			body:     `{"error":{"message":"Rate limit reached","type":"rate_limit_error"}}`,
			class:    "rate_limit",
		},
		{
			name:     "anthropic overloaded",
			path:     "/v1/messages",
			protocol: "anthropic",
			stream:   []string{`event: message_start`, `data: {"type":"message_start","message":{"id":"m","content":[]}}`, ``, `event: error`, `data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
			status:   529,
			body:     `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			class:    "upstream_5xx",
		},
		{
			name:     "numeric code wins",
			path:     "/v1/chat/completions",
			protocol: "openai-chat",
			stream:   []string{`data: {"error":{"code":503,"message":"backend unavailable","status":"UNAVAILABLE"}}`},
			status:   http.StatusServiceUnavailable,
			body:     `{"error":{"code":503,"message":"backend unavailable","status":"UNAVAILABLE"}}`,
			class:    "upstream_5xx",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := resolverForTest()
			res.ProviderModels[0].Protocol = tc.protocol
			up := &channelUpstream{responses: []Response{sseResponse(tc.stream...)}}
			reg := health.NewRegistry()
			rec := &usage.MemoryRecorder{}
			h := New(Config{Resolver: res, Upstream: up, Health: reg, Recorder: rec, MaxAttempts: 1})
			req := anthropicStreamRequest
			if tc.path == "/v1/chat/completions" {
				req = openAIStreamRequest
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(req)))
			if w.Code != tc.status || w.Body.String() != tc.body {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("an error must not be sent as an event stream, Content-Type=%q", ct)
			}
			if s := reg.Get("c"); s.WindowTotal != 1 || s.WindowSuccess != 0 {
				t.Fatalf("the failure must reach health once and no success: %+v", s)
			}
			if got := rec.Records(); len(got) != 1 || got[0].StatusCode != tc.status || got[0].ErrorClass != tc.class {
				t.Fatalf("usage must record the failure: %+v", got)
			}
		})
	}
	t.Run("empty stream", func(t *testing.T) {
		up := &channelUpstream{responses: []Response{sseResponse(`data: [DONE]`)}}
		h := New(Config{Resolver: resolverForTest(), Upstream: up, MaxAttempts: 1})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(openAIStreamRequest)))
		if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "provider_protocol") {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("cross protocol keeps the client's error shape", func(t *testing.T) {
		res := resolverForTest()
		res.ProviderModels[0].Protocol = "anthropic"
		up := &channelUpstream{responses: []Response{sseResponse(`event: error`, `data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)}}
		h := New(Config{Resolver: res, Upstream: up, MaxAttempts: 1})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(openAIStreamRequest)))
		var got struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 529 || got.Error.Type != "overloaded_error" || got.Error.Message != "Overloaded" {
			t.Fatalf("status=%d err=%v body=%s", w.Code, err, w.Body.String())
		}
	})
}

// A request the upstream rejects as invalid fails on every channel, so it must
// neither fail over nor count against the channel (same as an HTTP 400).
func TestStreamClientErrorEventDoesNotFailOver(t *testing.T) {
	up := &channelUpstream{responses: []Response{sseResponse(`data: {"error":{"message":"maximum context length exceeded","type":"invalid_request_error"}}`)}}
	reg := health.NewRegistry()
	h := New(Config{Resolver: twoChannelResolver("openai-chat"), Upstream: up, Health: reg})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(openAIStreamRequest)))
	if w.Code != http.StatusBadRequest || len(up.channels) != 1 {
		t.Fatalf("status=%d attempts=%v body=%s", w.Code, up.channels, w.Body.String())
	}
	if s := reg.Get(up.channels[0]); s.WindowTotal != 0 {
		t.Fatalf("a client error must not penalise the channel: %+v", s)
	}
}

// A healthy stream must reach the client byte for byte, and only once.
func TestStreamPassesThroughUnchangedAfterCommit(t *testing.T) {
	for _, protocol := range []string{"openai-chat", "anthropic"} {
		path, reqBody, good := "/v1/chat/completions", openAIStreamRequest, goodOpenAIStream
		if protocol == "anthropic" {
			path, reqBody, good = "/v1/messages", anthropicStreamRequest, goodAnthropicStream
		}
		res := resolverForTest()
		res.ProviderModels[0].Protocol = protocol
		up := &channelUpstream{responses: []Response{sseResponse(good...)}}
		reg := health.NewRegistry()
		h := New(Config{Resolver: res, Upstream: up, Health: reg})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(reqBody)))
		if want := strings.Join(good, "\n") + "\n"; w.Code != http.StatusOK || w.Body.String() != want {
			t.Fatalf("%s: status=%d\n got: %q\nwant: %q", protocol, w.Code, w.Body.String(), want)
		}
		if s := reg.Get("c"); len(up.channels) != 1 || s.WindowSuccess != 1 || s.WindowTotal != 1 {
			t.Fatalf("%s: attempts=%v health=%+v", protocol, up.channels, s)
		}
	}
}

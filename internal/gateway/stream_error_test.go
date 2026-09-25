package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relayhub/internal/health"
	"relayhub/internal/usage"
)

// AUDIT §5 B4/B5 follow-up: after the first output event the response is
// committed, so an error the upstream reports mid-stream cannot fail over any
// more. It must still reach the client instead of being swallowed: the
// protocol converters used to drop an "error" payload and finish the stream
// with a cheerful end_turn, leaving the client with an empty answer.
func TestOpenAIStreamErrorAfterFirstDeltaReachesAnthropicClient(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Par"}}]}`,
		``,
		`data: {"error":{"message":"upstream lost the connection","type":"server_error"}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	var out strings.Builder
	var meta streamMeta
	_, err := convertOpenAIStreamToAnthropic(body, "logical", func(s string) error { out.WriteString(s); return nil }, func() {}, &meta)
	if err == nil {
		t.Fatal("a mid-stream upstream error must be reported to the caller")
	}
	got := out.String()
	if !strings.Contains(got, `"text_delta","text":"Par"`) {
		t.Fatalf("the content before the error must be flushed: %s", got)
	}
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "upstream lost the connection") {
		t.Fatalf("the client must receive an Anthropic error event: %s", got)
	}
	if strings.Contains(got, "message_stop") || strings.Contains(got, "end_turn") {
		t.Fatalf("a failed stream must not be closed as a normal completion: %s", got)
	}
	if meta.ErrorEvent == "" {
		t.Fatal("the error must be recorded on the stream metadata")
	}
}

func TestOpenAIStreamErrorBeforeFirstDeltaIsNotRewritten(t *testing.T) {
	body := strings.NewReader("data: {\"error\":{\"message\":\"no quota\",\"type\":\"insufficient_quota\"}}\n")
	var out strings.Builder
	var meta streamMeta
	_, err := convertOpenAIStreamToAnthropic(body, "logical", func(s string) error { out.WriteString(s); return nil }, func() {}, &meta)
	if err == nil || out.Len() != 0 {
		t.Fatalf("nothing may be emitted before the first delta: err=%v out=%q", err, out.String())
	}
}

func TestAnthropicStreamErrorAfterFirstDeltaReachesOpenAIClient(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Par"}}`,
		``,
		`event: error`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		``,
	}, "\n"))
	var out strings.Builder
	var meta streamMeta
	_, err := convertAnthropicStreamToOpenAI(body, "logical", func(s string) error { out.WriteString(s); return nil }, func() {}, &meta)
	if err == nil {
		t.Fatal("a mid-stream upstream error must be reported to the caller")
	}
	got := out.String()
	if !strings.Contains(got, "Par") {
		t.Fatalf("the content before the error must be flushed: %s", got)
	}
	if !strings.Contains(got, `"error"`) || !strings.Contains(got, "Overloaded") {
		t.Fatalf("the client must receive an OpenAI error chunk: %s", got)
	}
	if strings.Contains(got, "[DONE]") {
		t.Fatalf("a failed stream must not end with [DONE]: %s", got)
	}
	if meta.ErrorEvent == "" {
		t.Fatal("the error must be recorded on the stream metadata")
	}
}

// Same-protocol streams are forwarded byte for byte: the error payload is the
// client's own protocol, so it is passed through and merely recorded.
func TestPipeSSERecordsErrorPayloads(t *testing.T) {
	lines := []string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Par"}}]}`,
		``,
		`data: {"error":{"message":"upstream exploded","type":"server_error"}}`,
		``,
		`data: [DONE]`,
	}
	body := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out strings.Builder
	var meta streamMeta
	_, err := pipeSSE(body, func(s string) error { out.WriteString(s); return nil }, func() {}, &meta)
	if err != errStreamErrorEvent {
		t.Fatalf("pipeSSE must report the upstream error, got %v", err)
	}
	if want := strings.Join(lines, "\n") + "\n"; out.String() != want {
		t.Fatalf("the stream must stay byte-identical\n got: %q\nwant: %q", out.String(), want)
	}
	if !strings.Contains(meta.ErrorEvent, "upstream exploded") {
		t.Fatalf("metadata: %+v", meta)
	}

	// A healthy stream is untouched.
	body = strings.NewReader(strings.Join(goodOpenAIStream, "\n") + "\n")
	out.Reset()
	meta = streamMeta{}
	if _, err := pipeSSE(body, func(s string) error { out.WriteString(s); return nil }, func() {}, &meta); err != nil || meta.ErrorEvent != "" {
		t.Fatalf("a healthy stream must not be flagged: err=%v meta=%+v", err, meta)
	}
}

// A mid-stream error penalises the channel exactly like a broken body does
// (RH-18): the 2xx headers were a success, the error event is a failure, and
// the usage record carries a class instead of looking like a normal answer.
func TestMidStreamErrorEventPenalisesTheChannel(t *testing.T) {
	up := &channelUpstream{responses: []Response{sseResponse(
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Par"}}]}`,
		``,
		`data: {"error":{"message":"upstream exploded","type":"server_error"}}`,
		``,
		`data: [DONE]`,
	)}}
	reg := health.NewRegistry()
	rec := &usage.MemoryRecorder{}
	h := New(Config{Resolver: resolverForTest(), Upstream: up, Health: reg, Recorder: rec, MaxAttempts: 2})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(openAIStreamRequest)))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "upstream exploded") {
		t.Fatalf("the upstream error must reach the client: status=%d body=%s", w.Code, w.Body.String())
	}
	if s := reg.Get("c"); s.WindowTotal != 2 || s.WindowSuccess != 1 || s.FailureCount != 1 {
		t.Fatalf("one success (headers) then one failure (body): %+v", s)
	}
	got := rec.Records()
	if len(got) != 1 || got[0].ErrorClass != "upstream_error_event" {
		t.Fatalf("usage must record the mid-stream failure: %+v", got)
	}
}

// Cross-protocol: the OpenAI client gets the Anthropic upstream's error in its
// own shape even though the stream had already produced content.
func TestMidStreamErrorEventCrossProtocolReachesTheClient(t *testing.T) {
	res := resolverForTest()
	res.ProviderModels[0].Protocol = "anthropic"
	up := &channelUpstream{responses: []Response{sseResponse(
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Par"}}`,
		``,
		`event: error`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		``,
	)}}
	rec := &usage.MemoryRecorder{}
	h := New(Config{Resolver: res, Upstream: up, Health: health.NewRegistry(), Recorder: rec, MaxAttempts: 1})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(openAIStreamRequest)))

	got := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(got, "Par") || !strings.Contains(got, "Overloaded") {
		t.Fatalf("status=%d body=%s", w.Code, got)
	}
	if strings.Contains(got, "[DONE]") {
		t.Fatalf("a failed stream must not end with [DONE]: %s", got)
	}
	if records := rec.Records(); len(records) != 1 || records[0].ErrorClass != "upstream_error_event" {
		t.Fatalf("usage must record the mid-stream failure: %+v", records)
	}
}

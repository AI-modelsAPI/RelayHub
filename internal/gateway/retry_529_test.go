package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relayhub/internal/health"
	"relayhub/internal/usage"
)

// AUDIT §5 B4 follow-up: Anthropic (and the relays in front of it) use 529 for
// "overloaded". The HTTP path only failed over on 401/429/5xx-class statuses
// listed in retryableStatus, so a 529 was handed straight to the client even
// though another channel could have served the request.
func TestRetryableStatusIncludesOverloaded(t *testing.T) {
	for _, code := range []int{401, 429, 500, 502, 503, 504, 529} {
		if !retryableStatus(code) {
			t.Errorf("status %d must be retryable", code)
		}
	}
	for _, code := range []int{400, 403, 404, 408, 413, 501, 530, 200} {
		if retryableStatus(code) {
			t.Errorf("status %d must not be retryable", code)
		}
	}
}

func TestOverloaded529FailsOver(t *testing.T) {
	overloaded := response(529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	good := response(http.StatusOK, `{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	up := &channelUpstream{responses: []Response{overloaded, good}}
	reg := health.NewRegistry()
	rec := &usage.MemoryRecorder{}
	h := New(Config{Resolver: twoChannelResolver("openai-chat"), Upstream: up, Health: reg, Recorder: rec})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[]}`)))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(up.channels) != 2 || up.channels[0] == up.channels[1] {
		t.Fatalf("expected a failover away from the overloaded channel, attempts went to %v", up.channels)
	}
	if s := reg.Get(up.channels[0]); s.WindowTotal != 1 || s.WindowSuccess != 0 {
		t.Fatalf("the overloaded channel must be penalised: %+v", s)
	}
	if s := reg.Get(up.channels[1]); s.WindowSuccess != 1 {
		t.Fatalf("the healthy channel must record a success: %+v", s)
	}
	if got := rec.Records(); len(got) != 1 || got[0].StatusCode != http.StatusOK {
		t.Fatalf("usage must record one success: %+v", got)
	}

	// With no alternative the client gets the upstream status itself.
	up = &channelUpstream{responses: []Response{overloaded}}
	h = New(Config{Resolver: resolverForTest(), Upstream: up, Health: health.NewRegistry(), MaxAttempts: 1})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[]}`)))
	if w.Code != 529 || !strings.Contains(w.Body.String(), "Overloaded") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

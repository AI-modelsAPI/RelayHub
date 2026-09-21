package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relayhub/internal/health"
	"relayhub/internal/usage"
)

// scriptedUpstream replays one step per attempt: an optional delay, then either
// an error or a response.
type scriptedUpstream struct {
	steps []scriptedStep
	calls int
}
type scriptedStep struct {
	delay time.Duration
	resp  Response
	err   error
}

func (s *scriptedUpstream) Do(_ context.Context, _ Request) (Response, error) {
	step := s.steps[s.calls]
	s.calls++
	if step.delay > 0 {
		time.Sleep(step.delay)
	}
	return step.resp, step.err
}

func responseWithHeader(status int, body string, hdr http.Header) Response {
	r := response(status, body)
	r.Header = hdr
	return r
}

// The latency/success-rate strategies are only as good as what the gateway
// feeds them. Before this test the success path recorded RecordSuccess(id, 0, 1)
// and the timer started after the upstream had already answered.
func TestGatewayRecordsMeasuredUpstreamLatency(t *testing.T) {
	up := &scriptedUpstream{steps: []scriptedStep{{delay: 30 * time.Millisecond, resp: response(200, `{"id":"x","model":"upstream","usage":{"prompt_tokens":1,"completion_tokens":1}}`)}}}
	reg := health.NewRegistry()
	rec := &usage.MemoryRecorder{}
	h := New(Config{Resolver: resolverForTest(), Upstream: up, Health: reg, Recorder: rec})
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	s := reg.Get("c")
	if s.Latency < 30*time.Millisecond {
		t.Fatalf("latency must include upstream time, got %v", s.Latency)
	}
	if s.WindowTotal != 1 || s.WindowSuccess != 1 || s.Status != health.Healthy {
		t.Fatalf("outcome not recorded: %+v", s)
	}
	if got := rec.Records(); len(got) != 1 || got[0].LatencyMS < 30 {
		t.Fatalf("usage latency must include upstream time: %+v", got)
	}
}

// A failure on the final attempt must still count against the channel; the
// old loop only recorded failures that were followed by a retry.
func TestGatewayRecordsFinalAttemptFailure(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		up := &scriptedUpstream{steps: []scriptedStep{{resp: response(503, `{"error":"down"}`)}}}
		reg := health.NewRegistry()
		rec := &usage.MemoryRecorder{}
		h := New(Config{Resolver: resolverForTest(), Upstream: up, Health: reg, Recorder: rec, MaxAttempts: 1})
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[]}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatalf("status=%d", w.Code)
		}
		if s := reg.Get("c"); s.WindowTotal != 1 || s.WindowSuccess != 0 {
			t.Fatalf("final failure not recorded: %+v", s)
		}
		if got := rec.Records(); len(got) != 1 || got[0].StatusCode != 503 || got[0].ErrorClass == "" {
			t.Fatalf("failed request must be recorded for stats: %+v", got)
		}
	})
	t.Run("network", func(t *testing.T) {
		up := &scriptedUpstream{steps: []scriptedStep{{err: errors.New("dial tcp: connection refused")}, {err: errors.New("dial tcp: connection refused")}}}
		reg := health.NewRegistry()
		h := New(Config{Resolver: resolverForTest(), Upstream: up, Health: reg, MaxAttempts: 2})
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[]}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("status=%d", w.Code)
		}
		// Single channel: the retry has nobody to fail over to, so exactly one
		// upstream attempt happened and it must be counted.
		if s := reg.Get("c"); s.WindowTotal != up.calls || s.WindowSuccess != 0 {
			t.Fatalf("network failure not recorded: calls=%d state=%+v", up.calls, s)
		}
	})
	t.Run("client error is not a channel failure", func(t *testing.T) {
		up := &scriptedUpstream{steps: []scriptedStep{{resp: response(400, `{"error":"bad request"}`)}}}
		reg := health.NewRegistry()
		h := New(Config{Resolver: resolverForTest(), Upstream: up, Health: reg})
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[]}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if s := reg.Get("c"); s.WindowTotal != 0 {
			t.Fatalf("4xx must not penalise the channel: %+v", s)
		}
	})
}

// 429/503 with Retry-After must put the channel in cooldown for that long
// (spec §6.3) instead of letting the next request hit it immediately.
func TestGatewayHonoursRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	hdr := http.Header{}
	hdr.Set("Retry-After", "120")
	up := &scriptedUpstream{steps: []scriptedStep{{resp: responseWithHeader(429, `{"error":"slow down"}`, hdr)}}}
	reg := health.NewRegistry()
	h := New(Config{Resolver: resolverForTest(), Upstream: up, Health: reg, MaxAttempts: 1, Now: func() time.Time { return now }})
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical","messages":[]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatalf("status=%d", w.Code)
	}
	s := reg.Get("c")
	if !s.CooldownUntil.Equal(now.Add(120 * time.Second)) {
		t.Fatalf("Retry-After not applied: %+v", s)
	}
	if reg.Available("c", now.Add(time.Minute)) {
		t.Fatal("channel must be unavailable during Retry-After window")
	}
	if !reg.Available("c", now.Add(3*time.Minute)) {
		t.Fatal("channel must recover after the window")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":        0,
		"garbage": 0,
		"0":       0,
		"-5":      0,
		"7":       7 * time.Second,
		"999999":  maxRetryAfter,
		now.Add(90 * time.Second).UTC().Format(http.TimeFormat): 90 * time.Second,
		now.Add(-time.Minute).UTC().Format(http.TimeFormat):     0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

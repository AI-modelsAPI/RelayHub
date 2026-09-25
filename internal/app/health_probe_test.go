package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/gateway"
	"relayhub/internal/health"
	"relayhub/internal/router"
	"relayhub/internal/verify"
)

func livenessResolver() *router.Resolver {
	res := &router.Resolver{}
	res.UpdateSnapshot(nil, map[string]domain.Channel{
		"c": {ID: "c", ProviderID: "p", Name: "Relay C", BaseURL: "http://c.invalid", Enabled: true, RoutingEnabled: true},
	}, map[string]domain.Model{
		"m": {ID: "m", Enabled: true},
	}, []domain.ProviderModel{
		{ID: "pm", ProviderID: "p", ChannelID: "c", ModelID: "m", UpstreamModelName: "gpt-4o", Protocol: "openai-chat", Enabled: true},
	}, nil, nil, nil)
	return res
}

// fakeLivenessUpstream answers the liveness request with a completion whose
// size is caught by tests.
type fakeLivenessUpstream struct {
	calls int
	reply gateway.Response
	err   error
	body  []byte
}

func (u *fakeLivenessUpstream) Do(_ context.Context, req gateway.Request) (gateway.Response, error) {
	u.calls++
	u.body = req.Body
	if u.err != nil {
		return gateway.Response{}, u.err
	}
	return u.reply, nil
}

func newHealthProbeFixture(t *testing.T, reply gateway.Response, err error, now time.Time) (*channelProber, *health.Registry, *fakeLivenessUpstream) {
	t.Helper()
	reg := health.NewRegistry()
	reg.SetRecoveryProbe(true)
	p := &channelProber{
		resolver: livenessResolver(),
		prober:   gateway.Prober{Upstream: &fakeLivenessUpstream{reply: reply, err: err}},
		verify:   verify.New(),
		repo:     fakeProbeRepo{channels: []domain.Channel{{ID: "c", ProviderID: "p", BaseURL: "http://c.invalid", Enabled: true, RoutingEnabled: true}}},
		health:   reg,
		logf:     func(string, ...any) {},
		spacing:  0,
		now:      func() time.Time { return now },
	}
	up := p.prober.Upstream.(*fakeLivenessUpstream)
	return p, reg, up
}

func TestHealthPassClosesATrippedChannelCheaply(t *testing.T) {
	tripAt := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	now := tripAt.Add(2 * time.Second)
	p, reg, up := newHealthProbeFixture(t, gateway.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"model":"gpt-4o","choices":[{"message":{"content":"P"},"finish_reason":"length"}],"usage":{"prompt_tokens":4}}`)),
	}, nil, now)

	reg.RecordFailure("c", 1, tripAt)
	if reg.Available("c", now) {
		t.Fatal("precondition: the gate must hold the tripped channel out")
	}
	p.healthPass(context.Background())

	if up.calls != 1 {
		t.Fatalf("one liveness request per tripped channel, got %d", up.calls)
	}
	var body map[string]any
	if err := json.Unmarshal(up.body, &body); err != nil || body["max_tokens"] != float64(1) {
		t.Fatalf("the recovery probe must stay cheap: %v (%s)", body, up.body)
	}
	s := reg.Get("c")
	if s.Status != health.Healthy || !reg.Available("c", now) {
		t.Fatalf("a successful probe must return the channel to routing: %+v", s)
	}
	// Nothing left to probe on the next pass.
	p.healthPass(context.Background())
	if up.calls != 1 {
		t.Fatalf("a healthy channel must not be probed again: %d calls", up.calls)
	}
}

func TestHealthPassFailureKeepsTheChannelOut(t *testing.T) {
	tripAt := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	now := tripAt.Add(2 * time.Second)
	p, reg, up := newHealthProbeFixture(t, gateway.Response{
		StatusCode: 529,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Overloaded"}}`)),
	}, nil, now)

	first := reg.RecordFailure("c", 1, tripAt)
	p.healthPass(context.Background())
	if up.calls != 1 {
		t.Fatalf("expected one probe, got %d", up.calls)
	}
	s := reg.Get("c")
	if s.Status != health.CircuitOpen || s.ConsecutiveTrips != 2 {
		t.Fatalf("a failed probe must re-open the breaker with backoff: %+v", s)
	}
	if !s.CooldownUntil.After(first.CooldownUntil) {
		t.Fatalf("the cooldown must grow: %s -> %s", first.CooldownUntil, s.CooldownUntil)
	}
	p.healthPass(context.Background())
	if up.calls != 1 {
		t.Fatalf("a channel inside its new cooldown must not be probed: %d calls", up.calls)
	}
	if reg.Available("c", s.CooldownUntil.Add(time.Millisecond)) {
		t.Fatal("with the gate on the channel must wait for the probe loop, not the clock")
	}
}

// A channel with no enabled binding cannot be probed: it must be left exactly
// as it was (not parked half-open, which would strand it).
func TestHealthPassLeavesUnprobeableChannelsAlone(t *testing.T) {
	tripAt := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	now := tripAt.Add(2 * time.Second)
	p, reg, up := newHealthProbeFixture(t, gateway.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil, now)
	p.resolver = &router.Resolver{}

	reg.RecordFailure("c", 1, tripAt)
	p.healthPass(context.Background())
	if up.calls != 0 {
		t.Fatalf("no bindings, no probe: %d calls", up.calls)
	}
	s := reg.Get("c")
	if s.Status != health.CircuitOpen || s.ProbeInFlight {
		t.Fatalf("the channel must be untouched: %+v", s)
	}
}

func TestFullStackWiresTheRecoveryProbe(t *testing.T) {
	start := func(t *testing.T, interval time.Duration) *Runtime {
		t.Helper()
		a, err := New(Config{DataDir: t.TempDir(), HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true, HealthProbeInterval: interval})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if err := a.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cancel()
			sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer scancel()
			_ = a.Shutdown(sctx)
		})
		return a.Runtime()
	}

	rt := start(t, time.Minute)
	if rt.healthProbePass == nil || rt.healthProbeEvery != time.Minute {
		t.Fatalf("the recovery loop must be wired: %+v", rt.healthProbeEvery)
	}
	if !rt.Health.RecoveryProbeEnabled() {
		t.Fatal("the breaker gate must be enabled while the loop runs")
	}

	off := start(t, 0)
	if off.healthProbeEvery != 0 || off.Health.RecoveryProbeEnabled() {
		t.Fatal("interval 0 must disable the loop and the gate, leaving the old breaker behaviour")
	}
}

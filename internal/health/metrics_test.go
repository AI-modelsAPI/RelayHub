package health

import (
	"testing"
	"time"
)

// TestRecordOutcomeSlidingWindow: success rate and latency must come from real
// observations, not from constants passed by the caller (the gateway used to
// call RecordSuccess(id, 0, 1) which made every channel look perfect).
func TestRecordOutcomeSlidingWindow(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	for i := 0; i < 8; i++ {
		r.RecordOutcome("c", true, 100*time.Millisecond, now)
	}
	for i := 0; i < 2; i++ {
		r.RecordOutcome("c", false, 0, now)
	}
	s := r.Get("c")
	if s.WindowTotal != 10 || s.WindowSuccess != 8 {
		t.Fatalf("window counts wrong: %+v", s)
	}
	if s.SuccessRate < 0.79 || s.SuccessRate > 0.81 {
		t.Fatalf("success rate must be 0.8 from observations, got %v", s.SuccessRate)
	}
	if s.Latency <= 0 || s.Latency > 100*time.Millisecond {
		t.Fatalf("latency must be derived from observed samples, got %v", s.Latency)
	}
	// Window is bounded: after many more successes the old failures fall out.
	for i := 0; i < WindowSize; i++ {
		r.RecordOutcome("c", true, 50*time.Millisecond, now)
	}
	s = r.Get("c")
	if s.SuccessRate != 1 || s.WindowTotal != WindowSize {
		t.Fatalf("window did not slide: %+v", s)
	}
}

// TestBreakerCooldownGrows: repeated trips must back off exponentially instead
// of a fixed 1s, otherwise a dead upstream is hammered every second forever.
func TestBreakerCooldownGrows(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	trip := func(at time.Time) time.Duration {
		var s State
		for i := 0; i < 3; i++ {
			s = r.RecordFailure("c", 3, at)
		}
		if s.Status != CircuitOpen {
			t.Fatalf("expected open, got %+v", s)
		}
		return s.CooldownUntil.Sub(at)
	}
	first := trip(now)
	if first != BaseCooldown {
		t.Fatalf("first cooldown must be %v, got %v", BaseCooldown, first)
	}
	// Probe allowed after cooldown, then fail again -> longer cooldown.
	after := now.Add(first + time.Millisecond)
	if !r.AllowProbe("c", after) {
		t.Fatal("probe should be allowed after cooldown")
	}
	second := trip(after)
	if second <= first {
		t.Fatalf("second cooldown %v must exceed first %v", second, first)
	}
	// Capped.
	at := after
	for i := 0; i < 20; i++ {
		at = at.Add(r.Get("c").CooldownUntil.Sub(at) + time.Millisecond)
		r.AllowProbe("c", at)
		trip(at)
	}
	if got := r.Get("c").CooldownUntil.Sub(at); got > MaxCooldown {
		t.Fatalf("cooldown %v exceeds cap %v", got, MaxCooldown)
	}
	// A success fully resets the backoff.
	r.RecordOutcome("c", true, time.Millisecond, at)
	if s := r.Get("c"); s.Status != Healthy || s.ConsecutiveTrips != 0 {
		t.Fatalf("success must reset breaker: %+v", s)
	}
}

// TestSetQuotaIsAdditive: quota updates must not wipe latency/success data, and
// a channel with a known zero balance must be excluded until it is refilled.
func TestSetQuotaIsAdditive(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.RecordOutcome("c", true, 40*time.Millisecond, now)
	r.SetQuota("c", QuotaUpdate{USD: 1.5, Remaining: 750000, Known: true}, now)
	s := r.Get("c")
	if s.QuotaUSD != 1.5 || !s.QuotaKnown || s.Latency == 0 || s.Status != Healthy {
		t.Fatalf("quota update clobbered state: %+v", s)
	}
	if !r.Available("c", now) {
		t.Fatal("funded channel must be available")
	}
	r.SetQuota("c", QuotaUpdate{USD: 0, Remaining: 0, Known: true}, now)
	if r.Available("c", now) {
		t.Fatal("zero balance must exclude the channel")
	}
	if r.Reason("c", now) != "quota exhausted" {
		t.Fatalf("reason: %q", r.Reason("c", now))
	}
	// Refill restores availability without a restart.
	r.SetQuota("c", QuotaUpdate{USD: 0.2, Remaining: 100000, Known: true}, now)
	if !r.Available("c", now) {
		t.Fatal("refilled channel must be available again")
	}
	// Unknown quota never excludes.
	r.SetQuota("d", QuotaUpdate{Known: false}, now)
	if !r.Available("d", now) {
		t.Fatal("unknown quota must not exclude")
	}
}

// TestObserverSeesTransitions: status changes are observable so they can be
// persisted and notified; steady-state updates must not spam the observer.
func TestObserverSeesTransitions(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	var events []Transition
	r.SetObserver(func(tr Transition) { events = append(events, tr) })
	r.RecordOutcome("c", true, time.Millisecond, now)
	r.RecordOutcome("c", true, time.Millisecond, now)
	for i := 0; i < 3; i++ {
		r.RecordFailure("c", 3, now)
	}
	r.RecordOutcome("c", true, time.Millisecond, now.Add(time.Hour))
	if len(events) != 3 {
		t.Fatalf("expected 3 transitions (->healthy, ->open, ->healthy), got %d: %+v", len(events), events)
	}
	if events[0].To != Healthy || events[1].To != CircuitOpen || events[2].To != Healthy {
		t.Fatalf("unexpected transition sequence: %+v", events)
	}
	if events[1].From != Healthy || events[1].ChannelID != "c" {
		t.Fatalf("transition metadata wrong: %+v", events[1])
	}
}

// TestMarkersDoNotWipeMetrics: legacy markers used to replace the whole State.
func TestMarkersDoNotWipeMetrics(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.RecordOutcome("c", true, 30*time.Millisecond, now)
	r.MarkAuthExpired("c")
	if s := r.Get("c"); s.Status != AuthExpired || s.Latency == 0 {
		t.Fatalf("MarkAuthExpired wiped metrics: %+v", s)
	}
	r.MarkQuotaExhausted("c", now.Add(time.Hour))
	if s := r.Get("c"); s.Status != QuotaExhausted || s.Latency == 0 || s.QuotaResetAt.IsZero() {
		t.Fatalf("MarkQuotaExhausted wiped metrics: %+v", s)
	}
	if r.Available("c", now) {
		t.Fatal("exhausted must be unavailable")
	}
}

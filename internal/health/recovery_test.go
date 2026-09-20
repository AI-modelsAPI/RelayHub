package health

import (
	"testing"
	"time"
)

// TestCircuitOpenRecoversAfterCooldown reproduces the production bug found in
// live testing: once a channel trips the circuit breaker, Available() returns
// false forever because nothing flips it back — the router permanently excludes
// the channel with "circuit open". After the cooldown elapses the breaker must
// allow traffic again (a probe), and a recorded success must fully close it.
func TestCircuitOpenRecoversAfterCooldown(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	// Trip the breaker (threshold 2).
	r.RecordFailure("c", 2, now)
	s := r.RecordFailure("c", 2, now)
	if s.Status != CircuitOpen {
		t.Fatalf("expected CircuitOpen, got %s", s.Status)
	}
	if r.Available("c", now) {
		t.Fatal("channel must be unavailable while circuit open + within cooldown")
	}
	// After cooldown elapses the breaker must let a request through again.
	after := s.CooldownUntil.Add(time.Millisecond)
	if !r.Available("c", after) {
		t.Fatal("circuit never recovers: still unavailable after cooldown elapsed")
	}
	// A success must reset it to Healthy (failure count cleared).
	r.RecordSuccess("c", time.Millisecond, 1)
	if got := r.Get("c"); got.Status != Healthy || got.FailureCount != 0 {
		t.Fatalf("success did not close circuit: %+v", got)
	}
}

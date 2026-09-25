package health

import (
	"testing"
	"time"
)

// AUDIT §5 B5: the breaker's half-open state existed but nothing in production
// drove it — after a cooldown elapsed a tripped channel was simply handed real
// client traffic again. With the recovery probe enabled the channel stays out
// of routing until a cheap synthetic request proves it works.

func tripped(t *testing.T, r *Registry, id string, now time.Time) State {
	t.Helper()
	r.RecordFailure(id, 2, now)
	s := r.RecordFailure(id, 2, now)
	if s.Status != CircuitOpen {
		t.Fatalf("expected the breaker to trip, got %s", s.Status)
	}
	return s
}

func TestRecoveryProbeGateHoldsTrippedChannelsOut(t *testing.T) {
	r := NewRegistry()
	r.SetRecoveryProbe(true)
	if !r.RecoveryProbeEnabled() {
		t.Fatal("the gate must report itself enabled")
	}
	now := time.Now()
	s := tripped(t, r, "c", now)
	after := s.CooldownUntil.Add(time.Millisecond)

	// Without the gate a tripped channel becomes routable again once the
	// cooldown elapses; with it, only a successful probe may release it.
	if r.Available("c", after) {
		t.Fatal("a tripped channel must stay out of routing while the recovery probe is on")
	}
	candidates := r.ProbeCandidates(after)
	if len(candidates) != 1 || candidates[0] != "c" {
		t.Fatalf("ProbeCandidates = %v, want [c]", candidates)
	}
	if len(r.ProbeCandidates(now)) != 0 {
		t.Fatalf("a channel still inside its cooldown must not be probed: %v", r.ProbeCandidates(now))
	}

	if !r.AllowProbe("c", after) {
		t.Fatal("AllowProbe must admit the candidate")
	}
	half := r.Get("c")
	if half.Status != CircuitHalfOpen || !half.ProbeInFlight || half.ProbeStartedAt.IsZero() {
		t.Fatalf("half-open state: %+v", half)
	}
	if r.Available("c", after) {
		t.Fatal("real traffic must wait while the probe is in flight")
	}
	if len(r.ProbeCandidates(after)) != 0 {
		t.Fatal("a channel with a probe in flight must not be probed twice")
	}
	// A wedged probe must not strand the channel forever.
	if !r.Available("c", after.Add(3*time.Minute)) {
		t.Fatal("a stale half-open probe must fall back to real traffic")
	}

	healthy := r.RecordOutcome("c", true, 12*time.Millisecond, after)
	if healthy.Status != Healthy || healthy.ProbeInFlight || !healthy.ProbeStartedAt.IsZero() {
		t.Fatalf("a successful probe must close the breaker: %+v", healthy)
	}
	if !r.Available("c", after) {
		t.Fatal("a healthy channel must be routable")
	}
}

func TestRecoveryProbeFailureReopensWithBackoff(t *testing.T) {
	r := NewRegistry()
	r.SetRecoveryProbe(true)
	now := time.Now()
	s := tripped(t, r, "c", now)
	after := s.CooldownUntil.Add(time.Millisecond)
	if !r.AllowProbe("c", after) {
		t.Fatal("AllowProbe must admit the candidate")
	}
	back := r.RecordOutcome("c", false, 0, after)
	if back.Status != CircuitOpen || back.ConsecutiveTrips != 2 {
		t.Fatalf("a failed probe must re-open the breaker with backoff: %+v", back)
	}
	if !back.CooldownUntil.After(s.CooldownUntil) {
		t.Fatalf("the second cooldown must be longer: %s -> %s", s.CooldownUntil, back.CooldownUntil)
	}
	if r.Available("c", after) || len(r.ProbeCandidates(after)) != 0 {
		t.Fatal("the channel must stay out until the new cooldown elapses")
	}
	if len(r.ProbeCandidates(back.CooldownUntil.Add(time.Millisecond))) != 1 {
		t.Fatal("the channel must be a candidate again after the new cooldown")
	}
}

func TestRecoveryProbeOffKeepsTheOldBehaviour(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	s := tripped(t, r, "c", now)
	after := s.CooldownUntil.Add(time.Millisecond)
	if !r.Available("c", after) {
		t.Fatal("without the probe loop the cooldown must release the channel (embedders rely on it)")
	}
	if len(r.ProbeCandidates(after)) != 1 {
		t.Fatal("candidates are still listed so an operator can trigger the probe")
	}
}

func TestProbeCandidatesAreSortedAndFiltered(t *testing.T) {
	r := NewRegistry()
	r.SetRecoveryProbe(true)
	now := time.Now()
	open := tripped(t, r, "b", now)
	tripped(t, r, "a", now)
	r.RecordSuccess("d", time.Millisecond, 1)
	after := open.CooldownUntil.Add(time.Millisecond)
	if got := r.ProbeCandidates(after); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("ProbeCandidates = %v, want [a b]", got)
	}
}

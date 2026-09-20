package health

import (
	"testing"
	"time"
)

func TestCooldown(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.CooldownFor("c", time.Minute, now)
	if r.Available("c", now.Add(30*time.Second)) {
		t.Fatal("available during cooldown")
	}
	if !r.Available("c", now.Add(2*time.Minute)) {
		t.Fatal("unavailable after cooldown")
	}
}
func TestCircuitTransitions(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.RecordFailure("c", 2, now)
	s := r.RecordFailure("c", 2, now)
	if s.Status != CircuitOpen || r.Available("c", now) {
		t.Fatalf("not open: %+v", s)
	}
	if r.AllowProbe("c", now) {
		t.Fatal("probe opened before cooldown")
	}
	if !r.AllowProbe("c", now.Add(2*time.Second)) {
		t.Fatal("half-open probe denied")
	}
	if r.Get("c").Status != CircuitHalfOpen {
		t.Fatal("not half-open")
	}
	r.RecordSuccess("c", time.Millisecond, 1)
	if r.Get("c").Status != Healthy {
		t.Fatal("not closed")
	}
}

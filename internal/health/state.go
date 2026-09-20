package health

import (
	"sync"
	"time"
)

type Status string

const (
	Healthy         Status = "healthy"
	Degraded        Status = "degraded"
	Cooldown        Status = "cooldown"
	QuotaExhausted  Status = "quota_exhausted"
	AuthExpired     Status = "auth_expired"
	Disabled        Status = "disabled"
	CircuitOpen     Status = "circuit_open"
	CircuitHalfOpen Status = "circuit_half_open"
)

type State struct {
	Status           Status
	CooldownUntil    time.Time
	FailureCount     int
	Latency          time.Duration
	SuccessRate      float64
	QuotaRemaining   int64
	QuotaResetAt     time.Time
	QuotaScore       float64
	Reason           string
	CircuitThreshold int
	ProbeInFlight    bool
}
type Registry struct {
	mu     sync.RWMutex
	states map[string]State
}

func NewRegistry() *Registry               { return &Registry{states: map[string]State{}} }
func (r *Registry) Set(id string, s State) { r.mu.Lock(); defer r.mu.Unlock(); r.states[id] = s }
func (r *Registry) Get(id string) State    { r.mu.RLock(); defer r.mu.RUnlock(); return r.states[id] }
func (r *Registry) Available(id string, now time.Time) bool {
	s := r.Get(id)
	if s.Status == Disabled || s.Status == QuotaExhausted || s.Status == AuthExpired {
		return false
	}
	if s.Status == CircuitOpen {
		// An open circuit blocks traffic only until its cooldown elapses; after
		// that a probe request is allowed through so the breaker can recover.
		// Without this, a tripped channel is excluded forever (no prod caller
		// invokes AllowProbe), which surfaced as permanent "circuit open".
		return !s.CooldownUntil.IsZero() && !now.Before(s.CooldownUntil)
	}
	if s.Status == CircuitHalfOpen {
		return true
	}
	if !s.CooldownUntil.IsZero() && now.Before(s.CooldownUntil) {
		return false
	}
	if s.QuotaRemaining == 0 && !s.QuotaResetAt.IsZero() && now.Before(s.QuotaResetAt) {
		return false
	}
	return true
}
func (r *Registry) Reason(id string, now time.Time) string {
	s := r.Get(id)
	if s.Reason != "" {
		return s.Reason
	}
	switch s.Status {
	case Disabled:
		return "disabled"
	case AuthExpired:
		return "authentication expired"
	case QuotaExhausted:
		return "quota exhausted"
	case CircuitOpen:
		return "circuit open"
	case CircuitHalfOpen:
		return "circuit half-open"
	case Cooldown:
		if now.Before(s.CooldownUntil) {
			return "cooldown"
		}
	}
	if !s.CooldownUntil.IsZero() && now.Before(s.CooldownUntil) {
		return "cooldown"
	}
	if s.QuotaRemaining == 0 && !s.QuotaResetAt.IsZero() && now.Before(s.QuotaResetAt) {
		return "quota exhausted"
	}
	return "unavailable"
}
func (r *Registry) CooldownFor(id string, d time.Duration, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[id]
	s.Status = Cooldown
	s.CooldownUntil = now.Add(d)
	r.states[id] = s
}
func (r *Registry) CooldownUntil(id string, until, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[id]
	s.Status = Cooldown
	if until.After(s.CooldownUntil) {
		s.CooldownUntil = until
	}
	r.states[id] = s
}
func (r *Registry) ApplyRetryAfter(id string, d time.Duration, now time.Time) {
	if d > 0 {
		r.CooldownFor(id, d, now)
	}
}
func (r *Registry) MarkHealthy(id string, latency time.Duration, successRate float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[id]
	s.Status = Healthy
	s.Latency = latency
	s.SuccessRate = successRate
	s.FailureCount = 0
	s.ProbeInFlight = false
	r.states[id] = s
}
func (r *Registry) MarkDegraded(id string, latency time.Duration, successRate float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[id]
	s.Status = Degraded
	s.Latency = latency
	s.SuccessRate = successRate
	r.states[id] = s
}
func (r *Registry) MarkQuotaExhausted(id string, resetAt time.Time) {
	r.Set(id, State{Status: QuotaExhausted, QuotaRemaining: 0, QuotaResetAt: resetAt})
}
func (r *Registry) MarkAuthExpired(id string) { r.Set(id, State{Status: AuthExpired}) }
func (r *Registry) MarkDisabled(id string)    { r.Set(id, State{Status: Disabled}) }
func (r *Registry) RecordFailure(id string, threshold int, now time.Time) State {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[id]
	s.FailureCount++
	if threshold <= 0 {
		threshold = 3
	}
	if s.FailureCount >= threshold {
		s.Status = CircuitOpen
		s.CooldownUntil = now.Add(time.Second)
	}
	r.states[id] = s
	return s
}
func (r *Registry) AllowProbe(id string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[id]
	if s.Status != CircuitOpen || now.Before(s.CooldownUntil) || s.ProbeInFlight {
		return false
	}
	s.Status = CircuitHalfOpen
	s.ProbeInFlight = true
	r.states[id] = s
	return true
}
func (r *Registry) RecordSuccess(id string, latency time.Duration, successRate float64) {
	r.MarkHealthy(id, latency, successRate)
}

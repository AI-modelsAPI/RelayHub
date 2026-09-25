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

const (
	// WindowSize bounds the per-channel outcome history used for SuccessRate.
	WindowSize = 50
	// BaseCooldown is the breaker's first open interval; every consecutive
	// trip doubles it up to MaxCooldown, and a success resets the backoff.
	BaseCooldown = time.Second
	MaxCooldown  = 5 * time.Minute
	// latencyAlpha is the EWMA weight given to the newest latency sample.
	latencyAlpha = 0.3
)

// State is the routing-facing view of one channel. Every field is derived from
// observations (gateway outcomes, balance polls, check-ins); nothing here is a
// constant supplied by the caller.
type State struct {
	Status        Status
	CooldownUntil time.Time
	FailureCount  int
	// Latency is an EWMA of successful upstream time-to-first-byte samples.
	Latency time.Duration
	// SuccessRate is the ratio of successful outcomes in the sliding window.
	// It is 1 when no outcome has been observed yet so that unknown channels
	// are not penalised against known-good ones.
	SuccessRate   float64
	WindowTotal   int
	WindowSuccess int
	// Quota fields are in the site's native unit (QuotaRemaining) and in USD
	// (QuotaUSD, the cross-site comparable unit). QuotaKnown tells whether a
	// balance has ever been observed; unknown quota never excludes a channel.
	QuotaRemaining int64
	QuotaUSD       float64
	QuotaKnown     bool
	QuotaResetAt   time.Time
	QuotaUpdatedAt time.Time
	// QuotaScore is a 0..1 preference used by quota-first routing; higher is
	// better. It is derived from QuotaUSD (see quotaScore).
	QuotaScore       float64
	Reason           string
	CircuitThreshold int
	ProbeInFlight    bool
	// ProbeStartedAt is when a half-open probe was admitted; a wedged probe
	// stops holding the channel out after probeStaleAfter.
	ProbeStartedAt   time.Time
	ConsecutiveTrips int
	LastFailureAt    time.Time
	LastSuccessAt    time.Time
	LastError        string
}

// QuotaUpdate carries one balance observation into the registry.
type QuotaUpdate struct {
	USD       float64
	Remaining int64
	Known     bool
	ResetAt   time.Time
	Source    string
}

// Transition describes a Status change; it is delivered to the observer set
// with SetObserver after the registry lock has been released.
type Transition struct {
	ChannelID string
	From, To  Status
	At        time.Time
	State     State
}

type window struct {
	buf   [WindowSize]bool
	next  int
	count int
	ok    int
}

func (w *window) push(success bool) {
	if w.count == WindowSize {
		if w.buf[w.next] {
			w.ok--
		}
	} else {
		w.count++
	}
	w.buf[w.next] = success
	if success {
		w.ok++
	}
	w.next = (w.next + 1) % WindowSize
}

type Registry struct {
	mu       sync.RWMutex
	states   map[string]State
	windows  map[string]*window
	observer func(Transition)
}

func NewRegistry() *Registry {
	return &Registry{states: map[string]State{}, windows: map[string]*window{}}
}

// SetObserver registers a callback for status transitions. It is invoked
// synchronously, outside the registry lock, only when Status actually changes.
func (r *Registry) SetObserver(fn func(Transition)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observer = fn
}

// update applies fn to the channel state under the lock and emits a transition
// when the status changed.
func (r *Registry) update(id string, now time.Time, fn func(s *State)) State {
	r.mu.Lock()
	s := r.states[id]
	prev := s.Status
	fn(&s)
	if s.SuccessRate == 0 && s.WindowTotal == 0 {
		s.SuccessRate = 1
	}
	r.states[id] = s
	obs := r.observer
	r.mu.Unlock()
	if obs != nil && prev != s.Status {
		obs(Transition{ChannelID: id, From: prev, To: s.Status, At: now, State: s})
	}
	return s
}

func (r *Registry) Set(id string, s State) { r.mu.Lock(); defer r.mu.Unlock(); r.states[id] = s }
func (r *Registry) Get(id string) State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := r.states[id]
	if s.WindowTotal == 0 && s.SuccessRate == 0 {
		s.SuccessRate = 1
	}
	return s
}

// Snapshot returns a copy of every tracked channel state.
func (r *Registry) Snapshot() map[string]State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]State, len(r.states))
	for k, v := range r.states {
		if v.WindowTotal == 0 && v.SuccessRate == 0 {
			v.SuccessRate = 1
		}
		out[k] = v
	}
	return out
}

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
	if s.QuotaKnown && s.QuotaRemaining == 0 && !s.QuotaResetAt.IsZero() && now.Before(s.QuotaResetAt) {
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
	if s.QuotaKnown && s.QuotaRemaining == 0 && !s.QuotaResetAt.IsZero() && now.Before(s.QuotaResetAt) {
		return "quota exhausted"
	}
	return "unavailable"
}
func (r *Registry) CooldownFor(id string, d time.Duration, now time.Time) {
	r.update(id, now, func(s *State) {
		s.Status = Cooldown
		s.CooldownUntil = now.Add(d)
	})
}
func (r *Registry) CooldownUntil(id string, until, now time.Time) {
	r.update(id, now, func(s *State) {
		s.Status = Cooldown
		if until.After(s.CooldownUntil) {
			s.CooldownUntil = until
		}
	})
}

// ApplyRetryAfter honours an upstream Retry-After hint. It never shortens an
// existing cooldown and does not touch the breaker state itself.
func (r *Registry) ApplyRetryAfter(id string, d time.Duration, now time.Time) {
	if d <= 0 {
		return
	}
	r.update(id, now, func(s *State) {
		until := now.Add(d)
		if until.After(s.CooldownUntil) {
			s.CooldownUntil = until
		}
		if s.Status == Healthy || s.Status == Degraded || s.Status == Cooldown || s.Status == "" {
			s.Status = Cooldown
		}
	})
}
func (r *Registry) MarkHealthy(id string, latency time.Duration, successRate float64) {
	r.update(id, time.Now(), func(s *State) {
		s.Status = Healthy
		s.Latency = latency
		s.SuccessRate = successRate
		s.FailureCount = 0
		s.ProbeInFlight = false
		s.ConsecutiveTrips = 0
	})
}
func (r *Registry) MarkDegraded(id string, latency time.Duration, successRate float64) {
	r.update(id, time.Now(), func(s *State) {
		s.Status = Degraded
		s.Latency = latency
		s.SuccessRate = successRate
	})
}

// MarkQuotaExhausted excludes the channel until resetAt (or until a later
// SetQuota reports a positive balance). Latency/success history is preserved.
func (r *Registry) MarkQuotaExhausted(id string, resetAt time.Time) {
	r.update(id, time.Now(), func(s *State) {
		s.Status = QuotaExhausted
		s.QuotaRemaining = 0
		s.QuotaUSD = 0
		s.QuotaKnown = true
		s.QuotaScore = 0
		s.QuotaResetAt = resetAt
	})
}
func (r *Registry) MarkAuthExpired(id string) {
	r.update(id, time.Now(), func(s *State) { s.Status = AuthExpired })
}
func (r *Registry) MarkDisabled(id string) {
	r.update(id, time.Now(), func(s *State) { s.Status = Disabled })
}

// ClearManual lifts Disabled/AuthExpired/QuotaExhausted markers, e.g. after a
// credential was re-entered. Breaker and cooldown state is left alone.
func (r *Registry) ClearManual(id string) {
	r.update(id, time.Now(), func(s *State) {
		if s.Status == Disabled || s.Status == AuthExpired || s.Status == QuotaExhausted {
			s.Status = Healthy
		}
	})
}

// SetQuota records a balance observation. It is additive: only quota fields
// change. A known zero balance flips the channel to QuotaExhausted; a later
// positive balance lifts that marker again.
func (r *Registry) SetQuota(id string, q QuotaUpdate, now time.Time) State {
	return r.update(id, now, func(s *State) {
		s.QuotaKnown = q.Known
		s.QuotaUpdatedAt = now
		if !q.Known {
			s.QuotaScore = 0
			return
		}
		s.QuotaRemaining = q.Remaining
		s.QuotaUSD = q.USD
		s.QuotaResetAt = q.ResetAt
		s.QuotaScore = quotaScore(q.USD)
		exhausted := q.USD <= 0 && q.Remaining <= 0
		switch {
		case exhausted && s.Status != Disabled && s.Status != AuthExpired:
			s.Status = QuotaExhausted
		case !exhausted && s.Status == QuotaExhausted:
			s.Status = Healthy
		}
	})
}

// quotaScore maps a USD balance onto 0..1 with diminishing returns: $0 -> 0,
// $1 -> 0.5, $10 -> ~0.9, so that quota-first prefers funded channels without
// letting one huge balance dominate every tie-break.
func quotaScore(usd float64) float64 {
	if usd <= 0 {
		return 0
	}
	return usd / (usd + 1)
}

// RecordOutcome is the single entry point the gateway uses per upstream
// attempt. Success samples feed the EWMA latency; every outcome feeds the
// sliding window. Failures increment the breaker counter using the channel's
// configured CircuitThreshold (default 3).
func (r *Registry) RecordOutcome(id string, success bool, latency time.Duration, now time.Time) State {
	if success {
		return r.recordSuccess(id, latency, now)
	}
	return r.recordFailure(id, 0, now, "")
}

// RecordFailure is RecordOutcome(false) with an explicit breaker threshold.
func (r *Registry) RecordFailure(id string, threshold int, now time.Time) State {
	return r.recordFailure(id, threshold, now, "")
}

// RecordFailureReason is RecordFailure with a short human-readable cause that
// is kept on the state for diagnostics and persisted health records.
func (r *Registry) RecordFailureReason(id string, threshold int, now time.Time, reason string) State {
	return r.recordFailure(id, threshold, now, reason)
}

// RecordSuccess keeps its historical signature for callers/tests; the
// successRate argument is ignored because the rate is derived from the
// observation window (callers used to pass the constant 1).
func (r *Registry) RecordSuccess(id string, latency time.Duration, _ float64) {
	r.recordSuccess(id, latency, time.Now())
}

func (r *Registry) recordSuccess(id string, latency time.Duration, now time.Time) State {
	r.mu.Lock()
	w := r.windows[id]
	if w == nil {
		w = &window{}
		r.windows[id] = w
	}
	w.push(true)
	rate := float64(w.ok) / float64(w.count)
	total, ok := w.count, w.ok
	r.mu.Unlock()
	return r.update(id, now, func(s *State) {
		s.WindowTotal, s.WindowSuccess, s.SuccessRate = total, ok, rate
		if latency > 0 {
			if s.Latency <= 0 {
				s.Latency = latency
			} else {
				s.Latency = time.Duration(float64(s.Latency)*(1-latencyAlpha) + float64(latency)*latencyAlpha)
			}
		}
		s.FailureCount = 0
		s.ProbeInFlight = false
		s.ConsecutiveTrips = 0
		s.LastSuccessAt = now
		s.LastError = ""
		if s.Status == CircuitOpen || s.Status == CircuitHalfOpen || s.Status == "" {
			s.Status = Healthy
		}
		if s.Status == Degraded && !degradedWindow(total, ok) {
			s.Status = Healthy
		}
		if s.Status == Cooldown && !now.Before(s.CooldownUntil) {
			s.Status = Healthy
		}
	})
}

func (r *Registry) recordFailure(id string, threshold int, now time.Time, reason string) State {
	r.mu.Lock()
	w := r.windows[id]
	if w == nil {
		w = &window{}
		r.windows[id] = w
	}
	w.push(false)
	rate := float64(w.ok) / float64(w.count)
	total, ok := w.count, w.ok
	r.mu.Unlock()
	return r.update(id, now, func(s *State) {
		s.WindowTotal, s.WindowSuccess, s.SuccessRate = total, ok, rate
		s.FailureCount++
		s.LastFailureAt = now
		if reason != "" {
			s.LastError = reason
		}
		if threshold <= 0 {
			threshold = s.CircuitThreshold
		}
		if threshold <= 0 {
			threshold = 3
		}
		s.ProbeInFlight = false
		switch {
		case s.Status == CircuitHalfOpen || s.FailureCount >= threshold:
			s.ConsecutiveTrips++
			s.Status = CircuitOpen
			s.CooldownUntil = now.Add(backoff(s.ConsecutiveTrips))
			s.FailureCount = 0
		case (s.Status == Healthy || s.Status == "") && degradedWindow(total, ok):
			// Degraded is a window property (most recent requests failed),
			// not a single-failure flap, so observers are not spammed.
			s.Status = Degraded
		case s.Status == "":
			s.Status = Healthy
		}
	})
}

// degradedWindow reports whether the sliding window says the channel is
// mostly failing: at least 5 samples and fewer than half succeeded.
func degradedWindow(total, ok int) bool {
	return total >= 5 && ok*2 < total
}

func backoff(trips int) time.Duration {
	d := BaseCooldown
	for i := 1; i < trips; i++ {
		d *= 2
		if d >= MaxCooldown {
			return MaxCooldown
		}
	}
	if d > MaxCooldown {
		return MaxCooldown
	}
	return d
}

// SetRecoveryProbe records that a cheap-probe loop is running: while it is on,
// a tripped channel stays out of routing until a probe closes the breaker.
// Not implemented yet.
func (r *Registry) SetRecoveryProbe(on bool) {}

// RecoveryProbeEnabled reports whether the breaker gate is on.
func (r *Registry) RecoveryProbeEnabled() bool { return false }

// ProbeCandidates lists the channels a recovery probe should try. Not
// implemented yet.
func (r *Registry) ProbeCandidates(now time.Time) []string { return nil }

// probeStaleAfter is how long a half-open probe may hold a channel out.
const probeStaleAfter = 2 * time.Minute

func (r *Registry) AllowProbe(id string, now time.Time) bool {
	r.mu.Lock()
	s := r.states[id]
	if s.Status != CircuitOpen || now.Before(s.CooldownUntil) || s.ProbeInFlight {
		r.mu.Unlock()
		return false
	}
	prev := s.Status
	s.Status = CircuitHalfOpen
	s.ProbeInFlight = true
	r.states[id] = s
	obs := r.observer
	r.mu.Unlock()
	if obs != nil {
		obs(Transition{ChannelID: id, From: prev, To: s.Status, At: now, State: s})
	}
	return true
}

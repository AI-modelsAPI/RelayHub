package ratelimit

import (
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limiter learns per-key token buckets from 429 / Retry-After / x-ratelimit-*.
type Limiter struct {
	mu    sync.Mutex
	now   func() time.Time
	slots map[string]*slot
}

type slot struct {
	rate    float64 // tokens per second
	tokens  float64
	updated time.Time
	retryAt time.Time
	learned bool
}

func New() *Limiter {
	return &Limiter{now: time.Now, slots: map[string]*slot{}}
}

func (l *Limiter) Observe(key string, status int, retryAfter time.Duration, hdr map[string][]string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.ensure(key)
	now := l.now()
	if status == 429 {
		s.rate = max(0.05, s.rate*0.5)
		if retryAfter > 0 {
			s.retryAt = now.Add(retryAfter)
		} else {
			s.retryAt = now.Add(time.Second)
		}
		s.tokens = 0
		s.learned = true
		s.updated = now
		return
	}
	if status >= 200 && status < 300 {
		if s.rate <= 0 {
			s.rate = 2
		} else {
			s.rate = min(50, s.rate+0.25)
		}
		s.learned = true
	}
	if rpm := headerInt(hdr, "x-ratelimit-limit-requests"); rpm > 0 {
		s.rate = float64(rpm) / 60
		s.learned = true
	}
	s.updated = now
}

// Wait returns how long to queue locally. Zero means proceed; >2s means caller should failover.
func (l *Limiter) Wait(key string) time.Duration {
	if l == nil || key == "" {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.ensure(key)
	now := l.now()
	if now.Before(s.retryAt) {
		return s.retryAt.Sub(now)
	}
	if s.rate <= 0 {
		return 0
	}
	elapsed := now.Sub(s.updated).Seconds()
	s.tokens = min(s.rate*2, s.tokens+elapsed*s.rate)
	s.updated = now
	if s.tokens >= 1 {
		s.tokens--
		return 0
	}
	need := (1 - s.tokens) / s.rate
	return time.Duration(need * float64(time.Second))
}

func (l *Limiter) Snapshot(key string) (rate float64, learned bool) {
	if l == nil {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.slots[key]
	if s == nil {
		return 0, false
	}
	return s.rate, s.learned
}

func (l *Limiter) ensure(key string) *slot {
	s := l.slots[key]
	if s == nil {
		s = &slot{rate: 4, tokens: 4, updated: l.now()}
		l.slots[key] = s
	}
	return s
}

func headerInt(h map[string][]string, name string) int {
	if h == nil {
		return 0
	}
	vals := h[httpCanonical(name)]
	if len(vals) == 0 {
		for k, v := range h {
			if strings.EqualFold(k, name) && len(v) > 0 {
				vals = v
				break
			}
		}
	}
	if len(vals) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(vals[0])
	return n
}

func httpCanonical(s string) string { return textproto.CanonicalMIMEHeaderKey(s) }

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

package affinity

import (
	"sync"
	"time"
)

type pin struct {
	channelID string
	keyID     string
	until     time.Time
}

// Table pins a session to a channel (and optional key) for the upstream
// prompt-cache TTL window.
type Table struct {
	TTL time.Duration
	now func() time.Time
	mu  sync.Mutex
	m   map[string]pin
}

func New(ttl time.Duration) *Table {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Table{TTL: ttl, now: time.Now, m: map[string]pin{}}
}

// maxPins caps the table so rejectable/unbounded session keys cannot grow
// memory without limit (AUDIT RH-32). Remember prunes expired entries first
// and then, if still full, evicts the pin closest to expiry.
const maxPins = 10000

func (t *Table) Remember(session, channelID, keyID string) {
	if t == nil || session == "" || channelID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if len(t.m) >= maxPins {
		for k, p := range t.m {
			if now.After(p.until) {
				delete(t.m, k)
			}
		}
	}
	if len(t.m) >= maxPins {
		var oldest string
		var oldestUntil time.Time
		first := true
		for k, p := range t.m {
			if first || p.until.Before(oldestUntil) {
				oldest, oldestUntil = k, p.until
				first = false
			}
		}
		delete(t.m, oldest)
	}
	t.m[session] = pin{channelID: channelID, keyID: keyID, until: now.Add(t.TTL)}
}

// Size reports the current pin count (diagnostics).
func (t *Table) Size() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.m)
}

func (t *Table) Lookup(session string) (channelID, keyID string, ok bool) {
	if t == nil || session == "" {
		return "", "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	p, exists := t.m[session]
	if !exists || t.now().After(p.until) {
		delete(t.m, session)
		return "", "", false
	}
	return p.channelID, p.keyID, true
}

func (t *Table) Forget(session string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.m, session)
	t.mu.Unlock()
}

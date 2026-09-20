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

func (t *Table) Remember(session, channelID, keyID string) {
	if t == nil || session == "" || channelID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[session] = pin{channelID: channelID, keyID: keyID, until: t.now().Add(t.TTL)}
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

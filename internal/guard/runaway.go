package guard

import (
	"hash/fnv"
	"sync"
	"time"
)

type event struct {
	hash uint64
	at   time.Time
}

type Guard struct {
	mu       sync.Mutex
	now      func() time.Time
	window   time.Duration
	limit    int
	sessions map[string][]event
}

func New() *Guard {
	return &Guard{now: time.Now, window: 2 * time.Minute, limit: 8, sessions: map[string][]event{}}
}

// Trip reports whether this session looks stuck in a loop (identical hashes).
func (g *Guard) Trip(session string, body []byte) bool {
	if g == nil || session == "" {
		return false
	}
	h := fnv.New64a()
	n := len(body)
	if n > 4096 {
		body = body[n-4096:]
	}
	_, _ = h.Write(body)
	sum := h.Sum64()
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	ev := g.sessions[session]
	cut := now.Add(-g.window)
	alive := ev[:0]
	same := 0
	for _, e := range ev {
		if e.at.Before(cut) {
			continue
		}
		alive = append(alive, e)
		if e.hash == sum {
			same++
		}
	}
	alive = append(alive, event{hash: sum, at: now})
	g.sessions[session] = alive
	return same >= g.limit
}

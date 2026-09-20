package lab

import (
	"sync"
)

type Capture struct {
	ID        string `json:"id"`
	Model     string `json:"model"`
	Protocol  string `json:"protocol"`
	ChannelID string `json:"channel_id"`
	Body      []byte `json:"-"`
	Size      int    `json:"size"`
}

type Ring struct {
	mu      sync.Mutex
	enabled bool
	max     int
	items   []Capture
	seq     int
}

func NewRing(n int) *Ring {
	if n <= 0 {
		n = 32
	}
	return &Ring{max: n}
}

func (r *Ring) SetEnabled(v bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.enabled = v
	r.mu.Unlock()
}

func (r *Ring) Enabled() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled
}

func (r *Ring) Push(c Capture) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return
	}
	r.seq++
	c.ID = "cap-" + itoa(r.seq)
	c.Size = len(c.Body)
	r.items = append(r.items, c)
	if len(r.items) > r.max {
		r.items = r.items[len(r.items)-r.max:]
	}
}

func (r *Ring) List() []Capture {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Capture, len(r.items))
	copy(out, r.items)
	for i := range out {
		out[i].Body = nil
	}
	return out
}

func (r *Ring) Get(id string) (Capture, bool) {
	if r == nil {
		return Capture{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.items {
		if c.ID == id {
			return c, true
		}
	}
	return Capture{}, false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

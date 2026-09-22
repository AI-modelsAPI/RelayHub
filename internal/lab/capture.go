package lab

import (
	"sync"
)

// maxCaptureBody caps a single captured request body (256 KiB).
const maxCaptureBody = 256 << 10

type Capture struct {
	ID        string `json:"id"`
	Model     string `json:"model"`
	Protocol  string `json:"protocol"`
	ChannelID string `json:"channel_id"`
	Body      []byte `json:"-"`
	Size      int    `json:"size"`
	// Truncated reports that Body was cut to maxCaptureBody; Size still
	// carries the original request length so callers can see the loss.
	Truncated bool `json:"truncated,omitempty"`
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
	// Captures keep full request bodies by design, but unbounded capture made
	// the ring a multi-hundred-MB privacy/memory risk (AUDIT RH-32): cap each
	// stored body and note truncation on the record.
	if len(c.Body) > maxCaptureBody {
		c.Body = c.Body[:maxCaptureBody]
		c.Truncated = true
	}
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

// Clear drops every stored capture. The enabled flag is left untouched so a
// DELETE on the lab endpoint empties the buffer without changing capture mode.
func (r *Ring) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = nil
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

package verify

import (
	"strings"
	"sync"

	"relayhub/internal/domain"
)

type Score struct {
	ChannelID string   `json:"channel_id"`
	Score     int      `json:"score"`
	Signals   []string `json:"signals"`
	// Passive is the score earned from live traffic alone.
	Passive int `json:"passive"`
	// Probes holds the latest conclusive authenticity probe per model.
	Probes []ProbeResult `json:"probes,omitempty"`
	// LastProbe is the most recent probe run, conclusive or not.
	LastProbe *ProbeResult `json:"last_probe,omitempty"`
	// Suspect is set while a fresh probe verdict demotes the channel.
	Suspect bool `json:"suspect"`
}

type Registry struct {
	mu      sync.Mutex
	passive map[string]Score
}

func New() *Registry {
	return &Registry{passive: map[string]Score{}}
}

func (r *Registry) Observe(rec domain.RequestRecord) Score {
	if r == nil {
		return Score{}
	}
	s := Score{ChannelID: rec.ChannelID, Score: 100}
	if rec.ChannelID == "" {
		return s
	}
	if rec.StatusCode >= 500 {
		s.Score -= 25
		s.Signals = append(s.Signals, "upstream_5xx")
	}
	if rec.StatusCode == 429 {
		s.Score -= 10
		s.Signals = append(s.Signals, "rate_limited")
	}
	if rec.FinishReason == "length" || rec.FinishReason == "max_tokens" {
		s.Score -= 15
		s.Signals = append(s.Signals, "truncated")
	}
	// Bidirectional, case-insensitive containment: providers legitimately echo
	// versioned names ("claude-3-5-sonnet" vs "claude-3-5-sonnet-20241022"),
	// which the old one-way Contains flagged as identity mismatches.
	if up, id := strings.ToLower(rec.UpstreamModel), strings.ToLower(rec.ModelID); up != "" && id != "" && !strings.Contains(up, id) && !strings.Contains(id, up) {
		s.Score -= 20
		s.Signals = append(s.Signals, "model_mismatch")
	}
	if rec.ErrorClass == "upstream_eof_no_finish" {
		s.Score -= 15
		s.Signals = append(s.Signals, "truncated")
	}
	if rec.InputTokens > 200 && rec.CacheReadTokens == 0 {
		s.Score -= 5
		s.Signals = append(s.Signals, "no_cache_tokens")
	}
	if rec.TTFTMS > 20000 {
		s.Score -= 10
		s.Signals = append(s.Signals, "slow_ttft")
	}
	if s.Score < 0 {
		s.Score = 0
	}
	r.mu.Lock()
	prev := r.passive[rec.ChannelID]
	if prev.Score > 0 {
		s.Score = (prev.Score*3 + s.Score) / 4
		s.Signals = mergeSignals(prev.Signals, s.Signals)
	}
	r.passive[rec.ChannelID] = s
	r.mu.Unlock()
	return s
}

func (r *Registry) All() []Score {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Score, 0, len(r.passive))
	for _, s := range r.passive {
		out = append(out, s)
	}
	return out
}

func (r *Registry) Get(channelID string) Score {
	if r == nil {
		return Score{ChannelID: channelID, Score: 100}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.passive[channelID]; ok {
		return s
	}
	return Score{ChannelID: channelID, Score: 100}
}

func mergeSignals(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string{}, a...), b...) {
		if seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

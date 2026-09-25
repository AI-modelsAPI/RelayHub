package verify

import (
	"strings"
	"sync"
	"time"

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

// Registry keeps per-channel trust: a passive score derived from live
// traffic and the verdicts of active authenticity probes (probe.go). Score
// is the worse of the passive score and every fresh probe verdict.
type Registry struct {
	mu        sync.Mutex
	passive   map[string]Score
	probes    map[string]map[string]ProbeResult // channel -> model -> latest conclusive probe
	lastProbe map[string]ProbeResult            // channel -> latest probe run
	baselines map[string]int                    // channel|protocol|upstream model -> first prompt size
	now       func() time.Time
}

func New() *Registry {
	r := &Registry{}
	r.ensureLocked()
	return r
}

func (r *Registry) ensureLocked() {
	if r.passive == nil {
		r.passive = map[string]Score{}
	}
	if r.probes == nil {
		r.probes = map[string]map[string]ProbeResult{}
	}
	if r.lastProbe == nil {
		r.lastProbe = map[string]ProbeResult{}
	}
	if r.baselines == nil {
		r.baselines = map[string]int{}
	}
}

func (r *Registry) clockLocked() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *Registry) Observe(rec domain.RequestRecord) Score {
	if r == nil {
		return Score{}
	}
	s := Score{ChannelID: rec.ChannelID, Score: 100}
	if rec.ChannelID == "" {
		s.Passive = s.Score
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
	if rec.ErrorClass == "upstream_error_event" {
		// The upstream answered 200 and then reported a failure inside the
		// stream: as bad as an HTTP 5xx, but invisible to the status check
		// (AUDIT §5 B5).
		s.Score -= 25
		s.Signals = append(s.Signals, "upstream_error_event")
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
	r.ensureLocked()
	prev := r.passive[rec.ChannelID]
	if prev.Score > 0 {
		s.Score = (prev.Score*3 + s.Score) / 4
		s.Signals = mergeSignals(prev.Signals, s.Signals)
	}
	s.Passive = s.Score
	r.passive[rec.ChannelID] = s
	r.mu.Unlock()
	return s
}

// All returns every channel with a passive score or a probe, sorted by ID.
func (r *Registry) All() []Score {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := map[string]bool{}
	for id := range r.passive {
		ids[id] = true
	}
	for id := range r.probes {
		ids[id] = true
	}
	for id := range r.lastProbe {
		ids[id] = true
	}
	out := make([]Score, 0, len(ids))
	for _, id := range sortedKeys(ids) {
		out = append(out, r.scoreLocked(id))
	}
	return out
}

func (r *Registry) Get(channelID string) Score {
	if r == nil {
		return Score{ChannelID: channelID, Score: 100, Passive: 100}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scoreLocked(channelID)
}

// scoreLocked combines the passive score with the fresh probe verdicts.
// Stale verdicts stay listed in Probes but no longer count.
func (r *Registry) scoreLocked(channelID string) Score {
	s, ok := r.passive[channelID]
	if !ok {
		s = Score{ChannelID: channelID, Score: 100}
	}
	s.Passive = s.Score
	s.Signals = append([]string(nil), s.Signals...)
	now := r.clockLocked()
	byModel := r.probes[channelID]
	for _, id := range sortedKeys(byModel) {
		p := byModel[id]
		s.Probes = append(s.Probes, p)
		if now.Sub(p.CheckedAt) > ProbeTTL {
			continue
		}
		if p.Score < s.Score {
			s.Score = p.Score
		}
		if p.Score < SuspectBelow {
			s.Suspect = true
		}
		// Probe findings first: mergeSignals caps the list.
		s.Signals = mergeSignals(p.Signals, s.Signals)
	}
	if lp, ok := r.lastProbe[channelID]; ok {
		s.LastProbe = &lp
	}
	return s
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

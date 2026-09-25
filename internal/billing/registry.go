package billing

import (
	"sort"
	"sync"
)

// Registry holds the newest report per channel. It is the shared state behind
// three consumers: the management API (what the console shows), the router
// (effective price as a routing signal) and the reconciler itself (verdict
// transitions, so one finding is announced once).
type Registry struct {
	mu      sync.RWMutex
	reports map[string]Report
}

func NewRegistry() *Registry {
	return &Registry{reports: make(map[string]Report)}
}

// Record stores the report and returns the previous one for the same channel,
// if any, so callers can announce only transitions into a finding.
func (r *Registry) Record(rep Report) (previous Report, had bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev, ok := r.reports[rep.ChannelID]
	r.reports[rep.ChannelID] = rep
	return prev, ok
}

// Last returns the newest report for a channel.
func (r *Registry) Last(channelID string) (Report, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rep, ok := r.reports[channelID]
	return rep, ok
}

// Reports returns every channel's newest report, sorted by channel ID.
func (r *Registry) Reports() []Report {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Report, 0, len(r.reports))
	for _, rep := range r.reports {
		out = append(out, rep)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChannelID < out[j].ChannelID })
	return out
}

// EffectiveUSDPerMTok implements the router's PriceSource: the observed price
// of a channel in USD per million tokens. A channel whose window produced no
// price (no traffic, no charges) reports "unknown" and is not ranked by cost.
func (r *Registry) EffectiveUSDPerMTok(channelID string) (float64, bool) {
	rep, ok := r.Last(channelID)
	if !ok || rep.Tokens <= 0 || rep.EffectiveUSDPerMTok <= 0 {
		return 0, false
	}
	return rep.EffectiveUSDPerMTok, true
}

// Summary aggregates the newest reports for display.
type Summary struct {
	Channels            int     `json:"channels"`
	OK                  int     `json:"ok"`
	Drift               int     `json:"drift"`
	Overcharge          int     `json:"overcharge"`
	Unknown             int     `json:"unknown"`
	Tokens              int64   `json:"tokens"`
	ChargedUSD          float64 `json:"charged_usd"`
	DeclaredUSD         float64 `json:"declared_usd"`
	SavingsUSD          float64 `json:"savings_usd"`
	EffectiveUSDPerMTok float64 `json:"effective_usd_per_mtok"`
}

// Summarize folds a report set into the numbers the console and the "today's
// savings" tile show.
func Summarize(reports []Report) Summary {
	var s Summary
	for _, rep := range reports {
		s.Channels++
		switch rep.Verdict {
		case VerdictOK:
			s.OK++
		case VerdictDrift:
			s.Drift++
		case VerdictOvercharge:
			s.Overcharge++
		default:
			s.Unknown++
		}
		s.Tokens += rep.Tokens
		s.ChargedUSD += rep.ChargedUSD
		s.DeclaredUSD += rep.DeclaredUSD
		s.SavingsUSD += rep.SavingsUSD
	}
	if s.Tokens > 0 {
		// Blended price across channels, weighted by local tokens.
		for _, rep := range reports {
			s.EffectiveUSDPerMTok += rep.EffectiveUSDPerMTok * float64(rep.Tokens)
		}
		s.EffectiveUSDPerMTok /= float64(s.Tokens)
	}
	return s
}

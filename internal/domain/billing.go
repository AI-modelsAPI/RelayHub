package domain

import "time"

// Multipliers are the billing ratios a new-api family site reports for one
// consumption log entry (the `other` field of GET /api/log/self). They are the
// site's own statement of which multiplier it applied, so a change between two
// reconciliation windows is direct evidence that a rate was raised. Known is
// false when the site reported no ratios at all (older builds, patched forks).
type Multipliers struct {
	ModelRatio      float64 `json:"model_ratio,omitempty"`
	GroupRatio      float64 `json:"group_ratio,omitempty"`
	CompletionRatio float64 `json:"completion_ratio,omitempty"`
	ModelPrice      float64 `json:"model_price,omitempty"`
	UserGroupRatio  float64 `json:"user_group_ratio,omitempty"`
	Known           bool    `json:"known"`
}

// TotalRatio is the product of the multipliers that scale a list price
// (model × group). It is 0 when nothing is known.
func (m Multipliers) TotalRatio() float64 {
	if !m.Known {
		return 0
	}
	total := 1.0
	if m.ModelRatio > 0 {
		total *= m.ModelRatio
	}
	if m.GroupRatio > 0 {
		total *= m.GroupRatio
	}
	return total
}

// BillingReport is one persisted reconciliation window for one channel (AUDIT
// §5 B2): what RelayHub counted locally versus what the site says it charged.
// It is metadata only — token counts, money and the site's own ratios; no
// prompt or response content is ever stored.
type BillingReport struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	// WindowStart / WindowEnd bound the reconciliation (one calendar day in
	// the site's billing timezone by default).
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	// Source names who ran the pass: "reconcile" (periodic) or "manual".
	Source string `json:"source,omitempty"`

	Requests        int   `json:"requests"`
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	Tokens          int64 `json:"tokens"`
	ChargedTokens   int64 `json:"charged_tokens"`

	ChargedQuota int64   `json:"charged_quota"`
	ChargedUSD   float64 `json:"charged_usd"`
	DeclaredUSD  float64 `json:"declared_usd"`
	// DeclaredKnown reports whether the catalog carried list prices for
	// enough of the window's tokens to compare against.
	DeclaredKnown bool `json:"declared_known"`

	EffectiveUSDPerMTok float64 `json:"effective_usd_per_mtok"`
	DeclaredUSDPerMTok  float64 `json:"declared_usd_per_mtok"`
	BaselineUSDPerMTok  float64 `json:"baseline_usd_per_mtok"`
	// Drift is the relative change of the effective price against the last
	// accepted (ok) observation: 0.5 means "50% more expensive than before".
	Drift           float64   `json:"drift"`
	TokenDrift      float64   `json:"token_drift"`
	MultiplierDrift float64   `json:"multiplier_drift"`
	SavingsUSD      float64   `json:"savings_usd"`
	GroupRatio      float64   `json:"group_ratio,omitempty"`
	ModelRatio      float64   `json:"model_ratio,omitempty"`
	Entries         int       `json:"entries"`
	MatchedModels   int       `json:"matched_models"`
	Truncated       bool      `json:"truncated"`
	Verdict         string    `json:"verdict"`
	Reason          string    `json:"reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

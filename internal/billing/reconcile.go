// Package billing reconciles RelayHub's local usage ledger against what the
// upstream site says it charged (AUDIT §5 B2).
//
// The local ledger is the gateway's own request history: token counts taken
// from the upstream's usage payloads. The site-side ledger is the consumption
// log a new-api family relay exposes (GET /api/log/self?type=2), which carries
// the quota it deducted per request plus the billing ratios it applied. From
// the two we derive an effective price (USD per million tokens), compare it
// with the catalog's list price and with the last accepted observation, and
// flag silent rate changes and overcharging.
//
// Everything here is pure: no HTTP, no database, no clock beyond the caller's.
// Fetching the site log lives in the adapters; persisting and scheduling lives
// in internal/app.
package billing

import (
	"fmt"
	"math"
	"time"

	"relayhub/internal/domain"
)

// Verdicts. A report is only ever "ok" when there is enough evidence for it;
// "unknown" means the window could not be judged, never "fine".
const (
	VerdictOK         = "ok"
	VerdictDrift      = "drift"
	VerdictOvercharge = "overcharge"
	VerdictUnknown    = "unknown"
)

// DefaultQuotaPerUnit is the new-api default ($1 = 500,000 quota) used when a
// charge carries no unit of its own.
const DefaultQuotaPerUnit int64 = 500000

// Window is the [Since, Until) interval a report covers.
type Window struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

// ModelUsage is one model's slice of the local ledger. InputPrice and
// OutputPrice are the catalog list prices in USD per million tokens (0 =
// unset); UpstreamName is what the channel calls the model, used to match the
// site's log entries.
type ModelUsage struct {
	ModelID         string  `json:"model_id"`
	UpstreamName    string  `json:"upstream_name,omitempty"`
	Requests        int     `json:"requests"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	InputPrice      float64 `json:"input_price,omitempty"`
	OutputPrice     float64 `json:"output_price,omitempty"`
}

// Ledger is the local (RelayHub-side) view of one channel's window.
type Ledger struct {
	ChannelID       string       `json:"channel_id"`
	Window          Window       `json:"window"`
	Requests        int          `json:"requests"`
	InputTokens     int64        `json:"input_tokens"`
	OutputTokens    int64        `json:"output_tokens"`
	CacheReadTokens int64        `json:"cache_read_tokens"`
	Models          []ModelUsage `json:"models"`
}

// Charge is one consumption entry from the site's own log.
type Charge struct {
	CreatedAt        time.Time          `json:"created_at"`
	ModelName        string             `json:"model_name"`
	Quota            int64              `json:"quota"`
	QuotaPerUnit     int64              `json:"quota_per_unit"`
	PromptTokens     int64              `json:"prompt_tokens"`
	CompletionTokens int64              `json:"completion_tokens"`
	Multipliers      domain.Multipliers `json:"multipliers"`
}

// Tokens is the entry's total token count.
func (c Charge) Tokens() int64 { return c.PromptTokens + c.CompletionTokens }

// USD converts the entry's deducted quota into dollars.
func (c Charge) USD() float64 {
	unit := c.QuotaPerUnit
	if unit <= 0 {
		unit = DefaultQuotaPerUnit
	}
	return float64(c.Quota) / float64(unit)
}

// Charges is the site-side input: the consumption log entries for the window
// plus whether the read was complete. A truncated read must never produce a
// verdict: an unfinished comparison is not evidence of anything.
type Charges struct {
	Entries   []Charge `json:"entries"`
	Truncated bool     `json:"truncated"`
}

// Baseline is the reference a window is compared against: the last observation
// whose price was accepted, so a rate change shows up as a jump.
type Baseline struct {
	Set                 bool               `json:"set"`
	At                  time.Time          `json:"at,omitempty"`
	EffectiveUSDPerMTok float64            `json:"effective_usd_per_mtok,omitempty"`
	Multipliers         domain.Multipliers `json:"multipliers,omitempty"`
}

// Observation is the smallest slice of a past report BaselineOf needs.
type Observation struct {
	At                  time.Time
	Verdict             string
	Tokens              int64
	EffectiveUSDPerMTok float64
	Multipliers         domain.Multipliers
}

// Params are the tolerances a verdict is judged with. Zero values take the
// documented defaults (see withDefaults).
type Params struct {
	// Threshold is the relative deviation tolerated before a window is
	// flagged: 0.15 = 15%.
	Threshold float64
	// MinTokens is the token count a window needs before it is judged.
	MinTokens int64
	// MinPricedCoverage is the share of the window's tokens that must carry
	// a catalog list price before the declared-price comparison is trusted.
	MinPricedCoverage float64
	// MaxTokenDrift is how much more the site may have counted than the
	// local ledger before the difference itself is flagged.
	MaxTokenDrift float64
}

func (p Params) withDefaults() Params {
	if p.Threshold <= 0 {
		p.Threshold = 0.15
	}
	if p.MinTokens <= 0 {
		p.MinTokens = 1000
	}
	if p.MinPricedCoverage <= 0 {
		p.MinPricedCoverage = 0.5
	}
	if p.MaxTokenDrift <= 0 {
		p.MaxTokenDrift = 0.25
	}
	return p
}

// Report is the outcome of reconciling one channel's window.
type Report struct {
	ChannelID string `json:"channel_id"`
	Window    Window `json:"window"`

	Requests        int   `json:"requests"`
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	// Tokens is the local total the report is based on (input + output).
	Tokens int64 `json:"tokens"`
	// ChargedTokens is the total the site recorded for the same window.
	ChargedTokens int64 `json:"charged_tokens"`

	ChargedQuota int64   `json:"charged_quota"`
	ChargedUSD   float64 `json:"charged_usd"`
	DeclaredUSD  float64 `json:"declared_usd"`
	// DeclaredKnown reports whether enough tokens carried a catalog price
	// for the declared-price comparison to be meaningful; DeclaredCoverage is
	// the share that did.
	DeclaredKnown    bool    `json:"declared_known"`
	DeclaredCoverage float64 `json:"declared_coverage"`

	EffectiveUSDPerMTok float64 `json:"effective_usd_per_mtok"`
	DeclaredUSDPerMTok  float64 `json:"declared_usd_per_mtok"`
	BaselineUSDPerMTok  float64 `json:"baseline_usd_per_mtok"`
	// Drift is effective / baseline - 1 (0 when there is no baseline).
	Drift      float64 `json:"drift"`
	TokenDrift float64 `json:"token_drift"`
	// SavingsUSD is what the catalog's list price would have cost minus what
	// the site actually charged, over the priced part of the window.
	SavingsUSD  float64            `json:"savings_usd"`
	Multipliers domain.Multipliers `json:"multipliers"`
	// MultiplierDrift is the largest relative change of a reported ratio
	// against the baseline (0 when either side is unknown).
	MultiplierDrift float64 `json:"multiplier_drift"`

	Entries       int  `json:"entries"`
	MatchedModels int  `json:"matched_models"`
	Truncated     bool `json:"truncated"`

	Verdict   string    `json:"verdict"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Bad reports whether the verdict is a finding the operator should hear about.
func (r Report) Bad() bool {
	return r.Verdict == VerdictDrift || r.Verdict == VerdictOvercharge
}

// Reconcile compares the local ledger with the site's charges and returns the
// verdict for the window. now is the report timestamp (injected for tests).
func Reconcile(ledger Ledger, site Charges, base Baseline, p Params, now time.Time) Report {
	p = p.withDefaults()
	rep := Report{
		ChannelID:       ledger.ChannelID,
		Window:          ledger.Window,
		Requests:        ledger.Requests,
		InputTokens:     ledger.InputTokens,
		OutputTokens:    ledger.OutputTokens,
		CacheReadTokens: ledger.CacheReadTokens,
		Tokens:          ledger.InputTokens + ledger.OutputTokens,
		Entries:         len(site.Entries),
		Truncated:       site.Truncated,
		CreatedAt:       now,
	}
	rep.DeclaredUSD, pricedTokens := declaredCost(ledger)
	if rep.Tokens > 0 && pricedTokens > 0 {
		rep.DeclaredCoverage = float64(pricedTokens) / float64(rep.Tokens)
		rep.DeclaredKnown = rep.DeclaredCoverage >= p.MinPricedCoverage
		rep.DeclaredUSDPerMTok = rep.DeclaredUSD / float64(rep.Tokens) * 1e6
	}
	for _, c := range site.Entries {
		rep.ChargedQuota += c.Quota
		rep.ChargedUSD += c.USD()
		rep.ChargedTokens += c.Tokens()
	}
	// The effective price comes from the site's own numbers: quota divided by
	// the tokens that quota was charged for. That stays correct when the local
	// ledger is incomplete (traffic that did not go through RelayHub).
	priceBasis := rep.ChargedTokens
	if priceBasis <= 0 {
		priceBasis = rep.Tokens
	}
	if priceBasis > 0 {
		rep.EffectiveUSDPerMTok = rep.ChargedUSD / float64(priceBasis) * 1e6
	}
	if rep.Tokens > 0 && rep.ChargedTokens > 0 {
		rep.TokenDrift = float64(rep.ChargedTokens)/float64(rep.Tokens) - 1
	}
	rep.MatchedModels = matchedModels(ledger, site.Entries)
	rep.Multipliers = dominantMultipliers(site.Entries)
	if base.Set {
		rep.BaselineUSDPerMTok = base.EffectiveUSDPerMTok
		if rep.BaselineUSDPerMTok > 0 && rep.EffectiveUSDPerMTok > 0 {
			rep.Drift = rep.EffectiveUSDPerMTok/rep.BaselineUSDPerMTok - 1
		}
		if base.Multipliers.Known && rep.Multipliers.Known {
			if d, _, ok := multiplierDrift(base.Multipliers, rep.Multipliers); ok {
				rep.MultiplierDrift = d
			}
		}
	}
	// Savings only count the tokens the catalog actually prices.
	if rep.DeclaredKnown {
		rep.SavingsUSD = rep.DeclaredUSD - rep.ChargedUSD*rep.DeclaredCoverage
	}
	rep.Verdict, rep.Reason = verdict(rep, base, p)
	return rep
}

// verdict applies the finding rules in priority order: something we can prove
// beats something we suspect, and "unknown" is preferred over a silent "ok"
// whenever the window could not actually be judged.
func verdict(rep Report, base Baseline, p Params) (string, string) {
	switch {
	case rep.Truncated:
		return VerdictUnknown, "站点日志超过取回上限，本窗口未覆盖完整"
	case rep.Entries == 0:
		return VerdictUnknown, "站点日志里没有本窗口的消费记录"
	case rep.Tokens < p.MinTokens:
		return VerdictUnknown, fmt.Sprintf("窗口内只有 %d 个 token，不做判定", rep.Tokens)
	}
	if rep.DeclaredKnown && rep.EffectiveUSDPerMTok > 0 && rep.TokenDrift <= p.MaxTokenDrift &&
		rep.EffectiveUSDPerMTok > rep.DeclaredUSDPerMTok*(1+p.Threshold) {
		return VerdictOvercharge, fmt.Sprintf("实际单价 $%.4f/1M 高于目录价 $%.4f/1M（+%.0f%%）",
			rep.EffectiveUSDPerMTok, rep.DeclaredUSDPerMTok, pct(rep.EffectiveUSDPerMTok/rep.DeclaredUSDPerMTok-1))
	}
	if base.Set && rep.BaselineUSDPerMTok > 0 && rep.EffectiveUSDPerMTok > 0 {
		d := rep.Drift
		if math.Abs(d) > p.Threshold {
			word := "上涨"
			if d < 0 {
				word = "下降"
			}
			return VerdictDrift, fmt.Sprintf("有效单价 $%.4f/1M 比上次正常值 $%.4f/1M %s %.0f%%",
				rep.EffectiveUSDPerMTok, base.EffectiveUSDPerMTok, word, pct(math.Abs(d)))
		}
	}
	if base.Set && base.Multipliers.Known && rep.Multipliers.Known && math.Abs(rep.MultiplierDrift) > p.Threshold {
		if name, before, after, ok := changedRatio(base.Multipliers, rep.Multipliers); ok {
			return VerdictDrift, fmt.Sprintf("站点倍率变化：%s %.4g → %.4g（%+.0f%%）",
				name, before, after, pct(rep.MultiplierDrift))
		}
	}
	if rep.TokenDrift > p.MaxTokenDrift {
		return VerdictOvercharge, fmt.Sprintf("站点记账 %d token，比本地统计的 %d 多 %.0f%%",
			rep.ChargedTokens, rep.Tokens, pct(rep.TokenDrift))
	}
	if rep.DeclaredUSD > 0 && !rep.DeclaredKnown {
		return VerdictUnknown, fmt.Sprintf("目录价只覆盖 %.0f%% 的 token，无法与站点计价比较",
			pct(rep.DeclaredCoverage))
	}
	return VerdictOK, fmt.Sprintf("有效单价 $%.4f/1M，与基线一致", rep.EffectiveUSDPerMTok)
}

// declaredCost sums what the catalog's list prices imply for the ledger's
// tokens and reports how many of those tokens carried a price at all.
func declaredCost(ledger Ledger) (usd float64, priced int64) {
	for _, m := range ledger.Models {
		if m.InputPrice > 0 {
			usd += float64(m.InputTokens) * m.InputPrice / 1e6
			priced += m.InputTokens
		}
		if m.OutputPrice > 0 {
			usd += float64(m.OutputTokens) * m.OutputPrice / 1e6
			priced += m.OutputTokens
		}
	}
	return usd, priced
}

// matchedModels counts the ledger's models that appear in the site's log, by
// logical ID or by upstream name.
func matchedModels(ledger Ledger, charges []Charge) int {
	if len(ledger.Models) == 0 || len(charges) == 0 {
		return 0
	}
	names := map[string]bool{}
	for _, c := range charges {
		if c.ModelName != "" {
			names[c.ModelName] = true
		}
	}
	n := 0
	for _, m := range ledger.Models {
		if names[m.ModelID] || (m.UpstreamName != "" && names[m.UpstreamName]) {
			n++
		}
	}
	return n
}

// dominantMultipliers returns the ratios of the log entry that best represents
// the window: the one with the most tokens that reports ratios at all.
func dominantMultipliers(charges []Charge) domain.Multipliers {
	var best domain.Multipliers
	bestTokens := int64(-1)
	for _, c := range charges {
		if !c.Multipliers.Known {
			continue
		}
		if t := c.Tokens(); t > bestTokens {
			best, bestTokens = c.Multipliers, t
		}
	}
	return best
}

// multiplierDrift returns the largest relative change between two ratio sets.
func multiplierDrift(before, after domain.Multipliers) (float64, string, bool) {
	worst, name, found := 0.0, "", false
	for _, pair := range ratioPairs(before, after) {
		if pair.before <= 0 || pair.after <= 0 {
			continue
		}
		d := pair.after/pair.before - 1
		if !found || math.Abs(d) > math.Abs(worst) {
			worst, name, found = d, pair.name, true
		}
	}
	return worst, name, found
}

// changedRatio returns the ratio pair that moved most, for the reason text.
func changedRatio(before, after domain.Multipliers) (string, float64, float64, bool) {
	worst, name, was, now, found := 0.0, "", 0.0, 0.0, false
	for _, pair := range ratioPairs(before, after) {
		if pair.before <= 0 || pair.after <= 0 {
			continue
		}
		d := pair.after/pair.before - 1
		if !found || math.Abs(d) > math.Abs(worst) {
			worst, name, was, now, found = d, pair.name, pair.before, pair.after, true
		}
	}
	return name, was, now, found
}

type ratioPair struct {
	name          string
	before, after float64
}

func ratioPairs(before, after domain.Multipliers) []ratioPair {
	return []ratioPair{
		{"分组倍率", before.GroupRatio, after.GroupRatio},
		{"模型倍率", before.ModelRatio, after.ModelRatio},
		{"补全倍率", before.CompletionRatio, after.CompletionRatio},
		{"模型单价", before.ModelPrice, after.ModelPrice},
		{"用户分组倍率", before.UserGroupRatio, after.UserGroupRatio},
	}
}

// BaselineOf picks the reference for drift detection: the most recent
// observation whose price was accepted ("ok"). When nothing has ever been
// accepted it falls back to the oldest finding, so a channel that starts out
// expensive is still compared against itself rather than against nothing.
func BaselineOf(obs []Observation, p Params) Baseline {
	p = p.withDefaults()
	usable := func(o Observation) bool { return o.EffectiveUSDPerMTok > 0 && o.Tokens >= p.MinTokens }
	var bestOK, oldest *Observation
	for i := range obs {
		o := obs[i]
		if !usable(o) || o.Verdict == VerdictUnknown {
			continue
		}
		if o.Verdict == VerdictOK {
			if bestOK == nil || o.At.After(bestOK.At) {
				c := o
				bestOK = &c
			}
			continue
		}
		if oldest == nil || o.At.Before(oldest.At) {
			c := o
			oldest = &c
		}
	}
	pick := bestOK
	if pick == nil {
		pick = oldest
	}
	if pick == nil {
		return Baseline{}
	}
	return Baseline{Set: true, At: pick.At, EffectiveUSDPerMTok: pick.EffectiveUSDPerMTok, Multipliers: pick.Multipliers}
}

// pct formats a relative change as a percentage number.
func pct(v float64) float64 { return math.Round(v*1000) / 10 }

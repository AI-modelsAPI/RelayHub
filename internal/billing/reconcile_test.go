package billing

import (
	"math"
	"strings"
	"testing"
	"time"

	"relayhub/internal/domain"
)

// Billing reconciliation (AUDIT §5 B2) must turn "what we counted" versus
// "what the site charged" into findings an operator can act on, and it must
// refuse to guess when the evidence is incomplete.

var (
	winStart = time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	winEnd   = winStart.Add(12 * time.Hour)
)

func testWindow() Window { return Window{Since: winStart, Until: winEnd} }

// charged builds a site log entry of `tokens` tokens charged `quota` units
// with the default new-api unit ($1 = 500,000).
func charged(name string, quota, tokens int64, mult domain.Multipliers) Charge {
	return Charge{
		CreatedAt: winStart.Add(time.Hour), ModelName: name,
		Quota: quota, QuotaPerUnit: 500000,
		PromptTokens: tokens, Multipliers: mult,
	}
}

func TestReconcileFlagsOverchargeAgainstCatalogPrice(t *testing.T) {
	ledger := Ledger{
		ChannelID: "c1", Window: testWindow(), Requests: 10, InputTokens: 1_000_000,
		Models: []ModelUsage{{
			ModelID: "m", UpstreamName: "claude-x", Requests: 10,
			InputTokens: 1_000_000, InputPrice: 1.0,
		}},
	}
	// The site charged 2,000,000 quota = $4 for 1M tokens, four times the
	// catalog's $1/1M.
	site := Charges{Entries: []Charge{charged("claude-x", 2_000_000, 1_000_000, domain.Multipliers{})}}

	rep := Reconcile(ledger, site, Baseline{}, Params{}, winEnd)

	if rep.Verdict != VerdictOvercharge || !rep.Bad() {
		t.Fatalf("verdict=%q reason=%q", rep.Verdict, rep.Reason)
	}
	if math.Abs(rep.ChargedUSD-4) > 1e-9 || math.Abs(rep.EffectiveUSDPerMTok-4) > 1e-9 {
		t.Fatalf("charged $%v at $%v/1M", rep.ChargedUSD, rep.EffectiveUSDPerMTok)
	}
	if !rep.DeclaredKnown || math.Abs(rep.DeclaredUSD-1) > 1e-9 || math.Abs(rep.DeclaredUSDPerMTok-1) > 1e-9 {
		t.Fatalf("declared known=%v usd=%v perMTok=%v", rep.DeclaredKnown, rep.DeclaredUSD, rep.DeclaredUSDPerMTok)
	}
	if rep.MatchedModels != 1 || rep.Entries != 1 {
		t.Fatalf("matched=%d entries=%d", rep.MatchedModels, rep.Entries)
	}
	if !strings.Contains(rep.Reason, "目录价") {
		t.Fatalf("reason must name the catalog price: %q", rep.Reason)
	}
}

func TestReconcileFlagsPriceDriftAgainstBaseline(t *testing.T) {
	ledger := Ledger{
		ChannelID: "c1", Window: testWindow(), InputTokens: 1_000_000,
		Models: []ModelUsage{{ModelID: "m", InputTokens: 1_000_000}},
	}
	// $1/1M now, $0.5/1M accepted earlier: the site doubled its rate.
	site := Charges{Entries: []Charge{charged("m", 500_000, 1_000_000, domain.Multipliers{})}}
	base := Baseline{Set: true, At: winStart.Add(-time.Hour), EffectiveUSDPerMTok: 0.5}

	rep := Reconcile(ledger, site, base, Params{}, winEnd)

	if rep.Verdict != VerdictDrift {
		t.Fatalf("verdict=%q reason=%q", rep.Verdict, rep.Reason)
	}
	if math.Abs(rep.Drift-1) > 1e-9 || math.Abs(rep.BaselineUSDPerMTok-0.5) > 1e-9 {
		t.Fatalf("drift=%v baseline=%v", rep.Drift, rep.BaselineUSDPerMTok)
	}
	if rep.DeclaredKnown {
		t.Fatal("no catalog price was supplied, so the declared comparison must stay unknown")
	}
	if !strings.Contains(rep.Reason, "0.5000") {
		t.Fatalf("reason must name the previous price: %q", rep.Reason)
	}
}

func TestReconcileFlagsMultiplierChange(t *testing.T) {
	ledger := Ledger{
		ChannelID: "c1", Window: testWindow(), InputTokens: 1_000_000,
		Models: []ModelUsage{{ModelID: "m", InputTokens: 1_000_000}},
	}
	// Same effective price as the baseline: the only evidence is the group
	// ratio the site reported (1 -> 1.5).
	site := Charges{Entries: []Charge{charged("m", 500_000, 1_000_000,
		domain.Multipliers{GroupRatio: 1.5, ModelRatio: 1, Known: true})}}
	base := Baseline{
		Set: true, EffectiveUSDPerMTok: 1,
		Multipliers: domain.Multipliers{GroupRatio: 1, ModelRatio: 1, Known: true},
	}

	rep := Reconcile(ledger, site, base, Params{}, winEnd)

	if rep.Verdict != VerdictDrift {
		t.Fatalf("verdict=%q reason=%q", rep.Verdict, rep.Reason)
	}
	if math.Abs(rep.MultiplierDrift-0.5) > 1e-9 {
		t.Fatalf("multiplier drift=%v", rep.MultiplierDrift)
	}
	if !strings.Contains(rep.Reason, "分组倍率") || !strings.Contains(rep.Reason, "1.5") {
		t.Fatalf("reason must name the changed ratio: %q", rep.Reason)
	}
}

func TestReconcileFlagsTokenInflation(t *testing.T) {
	ledger := Ledger{
		ChannelID: "c1", Window: testWindow(), InputTokens: 1_000_000,
		Models: []ModelUsage{{ModelID: "m", InputTokens: 1_000_000}},
	}
	// Same price per token as the baseline, but the site counted 1.5x the
	// tokens RelayHub saw for the same traffic.
	site := Charges{Entries: []Charge{charged("m", 1_500_000, 1_500_000, domain.Multipliers{})}}
	base := Baseline{Set: true, EffectiveUSDPerMTok: 2}

	rep := Reconcile(ledger, site, base, Params{}, winEnd)

	if rep.Verdict != VerdictOvercharge {
		t.Fatalf("verdict=%q reason=%q", rep.Verdict, rep.Reason)
	}
	if math.Abs(rep.TokenDrift-0.5) > 1e-9 || rep.ChargedTokens != 1_500_000 {
		t.Fatalf("token drift=%v charged=%d", rep.TokenDrift, rep.ChargedTokens)
	}
	if !strings.Contains(rep.Reason, "多") {
		t.Fatalf("reason must state the difference: %q", rep.Reason)
	}
}

func TestReconcileSaysUnknownWhenItCannotJudge(t *testing.T) {
	ledger := Ledger{
		ChannelID: "c1", Window: testWindow(), InputTokens: 1_000_000,
		Models: []ModelUsage{{ModelID: "m", InputTokens: 1_000_000}},
	}
	cases := []struct {
		name   string
		site   Charges
		reason string
	}{
		{"no charges", Charges{}, "没有本窗口"},
		{"truncated read", Charges{
			Entries:   []Charge{charged("m", 4_000_000, 1_000_000, domain.Multipliers{})},
			Truncated: true,
		}, "取回上限"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := Reconcile(ledger, tc.site, Baseline{}, Params{}, winEnd)
			if rep.Verdict != VerdictUnknown || rep.Bad() {
				t.Fatalf("verdict=%q", rep.Verdict)
			}
			if !strings.Contains(rep.Reason, tc.reason) {
				t.Fatalf("reason=%q want %q", rep.Reason, tc.reason)
			}
		})
	}

	t.Run("idle window", func(t *testing.T) {
		small := Ledger{
			ChannelID: "c1", Window: testWindow(), InputTokens: 10,
			Models: []ModelUsage{{ModelID: "m", InputTokens: 10}},
		}
		rep := Reconcile(small, Charges{Entries: []Charge{charged("m", 1000, 10, domain.Multipliers{})}}, Baseline{}, Params{}, winEnd)
		if rep.Verdict != VerdictUnknown || !strings.Contains(rep.Reason, "10 个 token") {
			t.Fatalf("verdict=%q reason=%q", rep.Verdict, rep.Reason)
		}
	})
}

func TestReconcileOKRecordsSavings(t *testing.T) {
	ledger := Ledger{
		ChannelID: "c1", Window: testWindow(), Requests: 4, InputTokens: 1_000_000,
		Models: []ModelUsage{{ModelID: "m", UpstreamName: "claude-x", Requests: 4, InputTokens: 1_000_000, InputPrice: 1.0}},
	}
	// The catalog lists $1/1M; the site charged $0.8 for the same tokens.
	site := Charges{Entries: []Charge{charged("claude-x", 400_000, 1_000_000, domain.Multipliers{})}}
	base := Baseline{Set: true, EffectiveUSDPerMTok: 0.8}

	rep := Reconcile(ledger, site, base, Params{}, winEnd)

	if rep.Verdict != VerdictOK || rep.Bad() {
		t.Fatalf("verdict=%q reason=%q", rep.Verdict, rep.Reason)
	}
	if math.Abs(rep.SavingsUSD-0.2) > 1e-9 {
		t.Fatalf("savings=$%v want $0.2", rep.SavingsUSD)
	}
	if math.Abs(rep.Drift) > 1e-9 {
		t.Fatalf("drift=%v", rep.Drift)
	}
}

func TestReconcileDeclaredComparisonNeedsCoverage(t *testing.T) {
	// Only 10% of the window's tokens carry a catalog price: a comparison
	// against the catalog would be meaningless, so the window stays unknown
	// instead of being called fine.
	ledger := Ledger{
		ChannelID: "c1", Window: testWindow(), InputTokens: 1_000_000,
		Models: []ModelUsage{
			{ModelID: "priced", InputTokens: 100_000, InputPrice: 1.0},
			{ModelID: "unpriced", InputTokens: 900_000},
		},
	}
	site := Charges{Entries: []Charge{charged("unpriced", 4_000_000, 1_000_000, domain.Multipliers{})}}

	rep := Reconcile(ledger, site, Baseline{}, Params{}, winEnd)

	if rep.DeclaredKnown {
		t.Fatalf("coverage %v must not count as known", rep.DeclaredCoverage)
	}
	if math.Abs(rep.DeclaredCoverage-0.1) > 1e-9 {
		t.Fatalf("coverage=%v want 0.1", rep.DeclaredCoverage)
	}
	if rep.Verdict != VerdictUnknown || !strings.Contains(rep.Reason, "目录价只覆盖") {
		t.Fatalf("verdict=%q reason=%q", rep.Verdict, rep.Reason)
	}
}

func TestBaselineOfPicksTheLastAcceptedObservation(t *testing.T) {
	obs := []Observation{
		{At: winStart.Add(3 * time.Hour), Verdict: VerdictUnknown, Tokens: 1_000_000, EffectiveUSDPerMTok: 9},
		{At: winStart.Add(time.Hour), Verdict: VerdictDrift, Tokens: 1_000_000, EffectiveUSDPerMTok: 2},
		{At: winStart.Add(2 * time.Hour), Verdict: VerdictOK, Tokens: 1_000_000, EffectiveUSDPerMTok: 1},
	}
	base := BaselineOf(obs, Params{})
	if !base.Set || math.Abs(base.EffectiveUSDPerMTok-1) > 1e-9 {
		t.Fatalf("baseline=%+v want the last accepted price 1", base)
	}

	// Nothing was ever accepted: the oldest finding becomes the reference,
	// so a channel that starts expensive still has a baseline.
	findings := []Observation{
		{At: winStart.Add(2 * time.Hour), Verdict: VerdictDrift, Tokens: 1_000_000, EffectiveUSDPerMTok: 3},
		{At: winStart.Add(time.Hour), Verdict: VerdictOvercharge, Tokens: 1_000_000, EffectiveUSDPerMTok: 2},
	}
	if base := BaselineOf(findings, Params{}); !base.Set || math.Abs(base.EffectiveUSDPerMTok-2) > 1e-9 {
		t.Fatalf("baseline=%+v want the oldest finding 2", base)
	}

	// Observations without a usable window never become a baseline.
	tiny := []Observation{{At: winStart, Verdict: VerdictOK, Tokens: 5, EffectiveUSDPerMTok: 1}}
	if base := BaselineOf(tiny, Params{}); base.Set {
		t.Fatalf("tiny window became a baseline: %+v", base)
	}
}

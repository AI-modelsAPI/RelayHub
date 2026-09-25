package billing

import (
	"math"
	"testing"
)

func TestRegistryKeepsNewestReportAndFeedsRouting(t *testing.T) {
	reg := NewRegistry()
	if _, had := reg.Record(Report{ChannelID: "a", Tokens: 1_000_000, EffectiveUSDPerMTok: 2, Verdict: VerdictOK}); had {
		t.Fatal("the first report for a channel has no predecessor")
	}
	prev, had := reg.Record(Report{ChannelID: "a", Tokens: 1_000_000, EffectiveUSDPerMTok: 3, Verdict: VerdictDrift})
	if !had || math.Abs(prev.EffectiveUSDPerMTok-2) > 1e-9 {
		t.Fatalf("previous report = %+v (had=%v)", prev, had)
	}
	if price, ok := reg.EffectiveUSDPerMTok("a"); !ok || math.Abs(price-3) > 1e-9 {
		t.Fatalf("price=%v ok=%v", price, ok)
	}
	if last, ok := reg.Last("a"); !ok || last.Verdict != VerdictDrift {
		t.Fatalf("last=%+v ok=%v", last, ok)
	}
}

func TestRegistryPriceIsUnknownWithoutTraffic(t *testing.T) {
	reg := NewRegistry()
	reg.Record(Report{ChannelID: "idle", Verdict: VerdictUnknown})
	if _, ok := reg.EffectiveUSDPerMTok("idle"); ok {
		t.Fatal("an idle channel must not report a price")
	}
	reg.Record(Report{ChannelID: "tokens", Tokens: 1000, EffectiveUSDPerMTok: 0, Verdict: VerdictUnknown})
	if _, ok := reg.EffectiveUSDPerMTok("tokens"); ok {
		t.Fatal("a report without a price must not be ranked by cost")
	}
	if _, ok := reg.EffectiveUSDPerMTok("missing"); ok {
		t.Fatal("an unknown channel must not report a price")
	}
}

func TestSummarizeCountsVerdictsAndMoney(t *testing.T) {
	reports := []Report{
		{ChannelID: "a", Verdict: VerdictOK, Tokens: 1_000_000, EffectiveUSDPerMTok: 1, ChargedUSD: 1, DeclaredUSD: 2, SavingsUSD: 1},
		{ChannelID: "b", Verdict: VerdictDrift, Tokens: 3_000_000, EffectiveUSDPerMTok: 2, ChargedUSD: 6, DeclaredUSD: 3},
		{ChannelID: "c", Verdict: VerdictOvercharge, Tokens: 0, EffectiveUSDPerMTok: 0, ChargedUSD: 0.5},
		{ChannelID: "d", Verdict: VerdictUnknown},
	}
	s := Summarize(reports)
	if s.Channels != 4 || s.OK != 1 || s.Drift != 1 || s.Overcharge != 1 || s.Unknown != 1 {
		t.Fatalf("counts: %+v", s)
	}
	if math.Abs(s.ChargedUSD-7.5) > 1e-9 || math.Abs(s.DeclaredUSD-5) > 1e-9 || math.Abs(s.SavingsUSD-1) > 1e-9 {
		t.Fatalf("money: %+v", s)
	}
	if s.Tokens != 4_000_000 {
		t.Fatalf("tokens=%d", s.Tokens)
	}
	if math.Abs(s.EffectiveUSDPerMTok-1.75) > 1e-9 { // (1*1M + 2*3M) / 4M
		t.Fatalf("blended price=%v want 1.75", s.EffectiveUSDPerMTok)
	}
}

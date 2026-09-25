package repository

import (
	"context"
	"testing"
	"time"

	"relayhub/internal/domain"
)

// Billing reports are the reconciliation history (AUDIT §5 B2): the console
// reads them back, and the baseline for "is this price normal?" comes from
// them, so every field the verdict was based on has to survive a round trip.
func TestBillingReportsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := timeNowUTC()
	start := now.Add(-4 * time.Hour)

	first := domain.BillingReport{
		ID: "br1", ChannelID: "c1", WindowStart: start, WindowEnd: now, Source: "reconcile",
		Requests: 12, InputTokens: 900_000, OutputTokens: 100_000, CacheReadTokens: 50_000,
		Tokens: 1_000_000, ChargedTokens: 1_200_000, ChargedQuota: 2_500_000,
		ChargedUSD: 5, DeclaredUSD: 2, DeclaredKnown: true,
		EffectiveUSDPerMTok: 4.1667, DeclaredUSDPerMTok: 2, BaselineUSDPerMTok: 2,
		Drift: 1.08, TokenDrift: 0.2, MultiplierDrift: 0.5, SavingsUSD: -1.4,
		GroupRatio: 1.5, ModelRatio: 2, Entries: 12, MatchedModels: 1, Truncated: true,
		Verdict: "overcharge", Reason: "实际单价高于目录价", CreatedAt: now,
	}
	if err := s.CreateBillingReport(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.ChannelID, second.Verdict, second.Truncated = "br2", "c2", "ok", false
	second.CreatedAt = now.Add(time.Minute)
	if err := s.CreateBillingReport(ctx, second); err != nil {
		t.Fatal(err)
	}

	// Newest first, and the channel filter is honoured.
	all, err := s.ListBillingReports(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != "br2" || all[1].ID != "br1" {
		t.Fatalf("all reports: %+v", all)
	}
	got, err := s.ListBillingReports(ctx, "c1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("channel filter returned %d rows", len(got))
	}
	row := got[0]
	if row.Requests != 12 || row.Tokens != 1_000_000 || row.ChargedTokens != 1_200_000 || row.ChargedQuota != 2_500_000 {
		t.Fatalf("counters: %+v", row)
	}
	if row.ChargedUSD != 5 || row.DeclaredUSD != 2 || row.SavingsUSD != -1.4 || row.EffectiveUSDPerMTok != 4.1667 {
		t.Fatalf("money: %+v", row)
	}
	if !row.DeclaredKnown || !row.Truncated || row.GroupRatio != 1.5 || row.ModelRatio != 2 {
		t.Fatalf("flags and ratios: %+v", row)
	}
	if row.Verdict != "overcharge" || row.Reason != "实际单价高于目录价" || row.Source != "reconcile" {
		t.Fatalf("verdict: %+v", row)
	}
	if !row.WindowStart.Equal(start) || !row.WindowEnd.Equal(now) || !row.CreatedAt.Equal(now) {
		t.Fatalf("times: %+v", row)
	}

	// The limit is a bound, not a hint.
	limited, err := s.ListBillingReports(ctx, "", 1)
	if err != nil || len(limited) != 1 || limited[0].ID != "br2" {
		t.Fatalf("limit: %v %+v", err, limited)
	}
}

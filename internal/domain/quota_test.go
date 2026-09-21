package domain

import (
	"testing"
	"time"
)

func TestQuotaSnapshotRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 21, 9, 30, 0, 0, time.UTC)
	reset := now.Add(24 * time.Hour)
	q := QuotaSnapshot{AvailableUSD: 1.25, UsedUSD: 3.5, Remaining: 625000, Total: 2375000, ResetAt: &reset, UpdatedAt: now, Source: "balance"}
	got, ok := ParseQuotaSnapshot(q.Encode())
	if !ok {
		t.Fatal("round trip must parse")
	}
	if got.AvailableUSD != 1.25 || got.Remaining != 625000 || !got.UpdatedAt.Equal(now) || got.ResetAt == nil || !got.ResetAt.Equal(reset) || got.Source != "balance" {
		t.Fatalf("mismatch: %+v", got)
	}
}

// Older releases stored arbitrary text in quota_state; it must not be treated
// as a balance and must not error.
func TestQuotaSnapshotToleratesLegacyText(t *testing.T) {
	for _, in := range []string{"", "ok", "remaining: 500", "{not json", `{"available_usd":1}`} {
		if _, ok := ParseQuotaSnapshot(in); ok {
			t.Fatalf("%q must not parse as a snapshot", in)
		}
	}
}

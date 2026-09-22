package app

import (
	"testing"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/health"
)

func TestSeedQuotaFromChannels(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	fresh := domain.QuotaSnapshot{AvailableUSD: 3, Remaining: 1500000, UpdatedAt: now.Add(-time.Hour), Source: "balance"}
	stale := domain.QuotaSnapshot{AvailableUSD: 9, Remaining: 1, UpdatedAt: now.Add(-72 * time.Hour), Source: "balance"}
	empty := domain.QuotaSnapshot{AvailableUSD: 0, Remaining: 0, UpdatedAt: now.Add(-time.Minute), Source: "checkin"}
	reg := health.NewRegistry()
	n := seedQuotaFromChannels(reg, map[string]domain.Channel{
		"fresh":  {ID: "fresh", QuotaState: fresh.Encode()},
		"stale":  {ID: "stale", QuotaState: stale.Encode()},
		"legacy": {ID: "legacy", QuotaState: "remaining 500"},
		"empty":  {ID: "empty", QuotaState: empty.Encode()},
	}, now)
	if n != 2 {
		t.Fatalf("expected 2 seeded channels, got %d", n)
	}
	if s := reg.Get("fresh"); !s.QuotaKnown || s.QuotaUSD != 3 {
		t.Fatalf("fresh snapshot not seeded: %+v", s)
	}
	if s := reg.Get("stale"); s.QuotaKnown {
		t.Fatalf("stale snapshot must be ignored: %+v", s)
	}
	if s := reg.Get("legacy"); s.QuotaKnown {
		t.Fatalf("legacy text must be ignored: %+v", s)
	}
	if reg.Available("empty", now) {
		t.Fatal("a fresh zero balance must exclude the channel at startup")
	}
}

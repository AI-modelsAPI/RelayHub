package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/health"
	"relayhub/internal/notify"
)

func TestHealthAndQuotaNotifications(t *testing.T) {
	var mu sync.Mutex
	var kinds []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p map[string]any
		_ = json.Unmarshal(b, &p)
		mu.Lock()
		kinds = append(kinds, p["kind"].(string)+":"+p["title"].(string))
		mu.Unlock()
	}))
	defer srv.Close()
	d := notify.NewDispatcher(notify.Options{}, notify.NewWebhook(srv.URL, srv.Client()))
	label := func(id string) string { return "SeekAI(" + id + ")" }

	reg := health.NewRegistry()
	now := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	reg.SetObserver(composeHealthObservers(healthPersister(&memHealthRepo{}, nil), healthNotifier(d, label)))
	for i := 0; i < 3; i++ {
		reg.RecordFailureReason("c", 3, now, "upstream status 503")
	}
	reg.AllowProbe("c", now.Add(2*time.Second))
	reg.RecordOutcome("c", true, time.Millisecond, now.Add(3*time.Second))

	quota := quotaLowNotifier(d, 0) // default threshold
	quota(domain.Channel{ID: "c", Name: "SeekAI"}, domain.QuotaSnapshot{AvailableUSD: 0.2, UpdatedAt: now, Source: "balance"})
	quota(domain.Channel{ID: "c", Name: "SeekAI"}, domain.QuotaSnapshot{AvailableUSD: 3, UpdatedAt: now, Source: "balance"}) // above threshold
	quota(domain.Channel{ID: "d"}, domain.QuotaSnapshot{})                                                                   // unknown
	d.Flush(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"circuit_open:渠道熔断：SeekAI(c)",
		"channel_recovered:渠道恢复：SeekAI(c)",
		"quota_low:额度告急：SeekAI",
	}
	if len(kinds) != len(want) {
		t.Fatalf("events=%v want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("event %d = %q want %q (all: %v)", i, kinds[i], want[i], kinds)
		}
	}
}

func TestRepoChannelLookupFallsBackToID(t *testing.T) {
	if got := repoChannelLookup(nil)("abc"); got != "abc" {
		t.Fatalf("nil repo must return id, got %q", got)
	}
}

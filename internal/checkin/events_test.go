package checkin

import (
	"context"
	"sync"
	"testing"
	"time"

	"relayhub/internal/adapter"
	"relayhub/internal/domain"
	"relayhub/internal/notify"
)

// Unattended check-in farms only work if failures reach a human: the
// scheduler must emit events for final failures and manual fallbacks.
func TestSchedulerEmitsEventsForFailures(t *testing.T) {
	repo := newBalanceRepo(t)
	ctx := context.Background()
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "fail", Enabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "Fails", BaseURL: "https://x", Enabled: true, CheckinEnabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "m", ProviderID: "p", Name: "Manual", BaseURL: "https://x", Enabled: true, CheckinEnabled: true, CheckinMode: "manual"})
	reg := adapter.NewRegistry()
	_ = reg.Register("fail", &mockFailingAdapter{})
	sched := NewScheduler(Config{Interval: time.Hour, RandomJitter: time.Second, BaseBackoff: time.Millisecond, MaxRetries: 1}, reg, repo)

	var mu sync.Mutex
	var events []notify.Event
	sched.SetEventSink(func(ev notify.Event) { mu.Lock(); events = append(events, ev); mu.Unlock() })

	_ = sched.RunNow(ctx, "c")
	_ = sched.RunNow(ctx, "m")

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("expected 2 events (failed + need_manual), got %+v", events)
	}
	if events[0].Kind != notify.KindCheckinFailed || events[0].ChannelID != "c" || events[0].Severity != notify.SeverityError || events[0].Body == "" {
		t.Fatalf("failed event wrong: %+v", events[0])
	}
	if events[1].Kind != notify.KindNeedManual || events[1].ChannelID != "m" {
		t.Fatalf("need_manual event wrong: %+v", events[1])
	}
	for _, ev := range events {
		if ev.Title == "" || ev.At.IsZero() {
			t.Fatalf("event must carry title and time: %+v", ev)
		}
	}
}

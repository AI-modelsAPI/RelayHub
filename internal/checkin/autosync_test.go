package checkin

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"relayhub/internal/adapter"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
)

// fakeSyncRepo is a minimal ResourceRepository stub exposing only the channel
// listing the scheduler's auto-sync path touches. Embedding the interface makes
// unimplemented methods panic if unexpectedly called.
type fakeSyncRepo struct {
	repository.ResourceRepository
	channels []domain.Channel
}

func (f *fakeSyncRepo) ListChannels(context.Context, string) ([]domain.Channel, error) {
	return f.channels, nil
}

func TestSchedulerAutoSyncTriggersOnlyForEnabledAutoSyncChannels(t *testing.T) {
	repo := &fakeSyncRepo{channels: []domain.Channel{
		{ID: "c1", Enabled: true, AutoSync: true, AutoSyncPattern: "gpt"},
		{ID: "c2", Enabled: true, AutoSync: false}, // auto_sync off -> skip
		{ID: "c3", Enabled: false, AutoSync: true}, // disabled -> skip
	}}
	s := NewScheduler(Config{Interval: time.Hour, RandomJitter: time.Minute}, adapter.NewRegistry(), repo)

	var calls atomic.Int64
	gotPattern := make(chan string, 4)
	s.SetModelSync(func(ctx context.Context, channelID, pattern string) error {
		calls.Add(1)
		if channelID == "c1" {
			gotPattern <- pattern
		}
		return nil
	})

	s.checkAndTrigger(context.Background())

	select {
	case p := <-gotPattern:
		if p != "gpt" {
			t.Fatalf("expected pattern 'gpt' for c1, got %q", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("auto-sync callback was not invoked for c1")
	}

	// Give any erroneous goroutines a moment, then assert only c1 fired.
	time.Sleep(100 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 auto-sync call (c1 only), got %d", got)
	}

	// Second immediate trigger must NOT re-fire (within Interval cadence).
	s.checkAndTrigger(context.Background())
	time.Sleep(100 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("auto-sync re-fired before interval elapsed: calls=%d", got)
	}
}

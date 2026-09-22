package checkin

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"relayhub/internal/adapter"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

type balanceAdapter struct {
	mockSuccessfulAdapter
	mu       sync.Mutex
	calls    int
	result   adapter.BalanceResult
	err      error
	checkins int
}

func (b *balanceAdapter) Balance(ctx context.Context, ch domain.Channel) (adapter.BalanceResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	return b.result, b.err
}
func (b *balanceAdapter) CheckIn(ctx context.Context, ch domain.Channel) (adapter.CheckInResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checkins++
	return adapter.CheckInResult{Success: true, Already: b.checkins > 1, RewardUSD: 0.5, RewardKnown: true, Message: "ok"}, nil
}
func (b *balanceAdapter) count() int { b.mu.Lock(); defer b.mu.Unlock(); return b.calls }

func newBalanceRepo(t *testing.T) *repository.Store {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "balance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return repository.New(db.DB)
}

// P0-B: the quota-aware strategy had no writer. After a successful check-in the
// scheduler must fetch the balance, persist a structured snapshot on the
// channel and hand it to the quota observer (health registry in production).
func TestRunNowPersistsQuotaAndNotifiesObserver(t *testing.T) {
	repo := newBalanceRepo(t)
	ctx := context.Background()
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "bal", Enabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://x", Enabled: true, CheckinEnabled: true})

	adp := &balanceAdapter{result: adapter.BalanceResult{Remaining: 750000, Total: 1000000, AvailableUSD: 1.5, UsedUSD: 0.5, QuotaPerUnit: 500000, Username: "u"}}
	reg := adapter.NewRegistry()
	_ = reg.Register("bal", adp)
	sched := NewScheduler(Config{Interval: time.Hour, RandomJitter: time.Second, BaseBackoff: time.Millisecond}, reg, repo)

	var observed []domain.QuotaSnapshot
	var mu sync.Mutex
	sched.SetQuotaObserver(func(ch domain.Channel, q domain.QuotaSnapshot) {
		mu.Lock()
		defer mu.Unlock()
		observed = append(observed, q)
	})

	if err := sched.RunNow(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if adp.count() != 1 {
		t.Fatalf("balance must be fetched once after check-in, got %d", adp.count())
	}
	ch, _ := repo.GetChannel(ctx, "c")
	q, ok := domain.ParseQuotaSnapshot(ch.QuotaState)
	if !ok {
		t.Fatalf("quota_state must be a structured snapshot, got %q", ch.QuotaState)
	}
	if q.AvailableUSD != 1.5 || q.Remaining != 750000 || q.Source != "checkin" || q.Username != "u" {
		t.Fatalf("snapshot content: %+v", q)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 1 || observed[0].AvailableUSD != 1.5 {
		t.Fatalf("observer must see the snapshot: %+v", observed)
	}
	st := sched.GetState("c")
	if st.QuotaUSD != 1.5 || st.LastBalanceAt.IsZero() || st.LastBalanceError != "" {
		t.Fatalf("job state must expose balance: %+v", st)
	}
}

// Balance failures must not turn a successful check-in into a failure, but
// they must be visible.
func TestRunNowBalanceFailureIsVisibleNotFatal(t *testing.T) {
	repo := newBalanceRepo(t)
	ctx := context.Background()
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "bal", Enabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://x", Enabled: true, CheckinEnabled: true, QuotaState: "legacy text"})
	adp := &balanceAdapter{err: errors.New("balance request failed with status 502")}
	reg := adapter.NewRegistry()
	_ = reg.Register("bal", adp)
	sched := NewScheduler(Config{Interval: time.Hour, RandomJitter: time.Second, BaseBackoff: time.Millisecond}, reg, repo)
	if err := sched.RunNow(ctx, "c"); err != nil {
		t.Fatalf("check-in must still succeed: %v", err)
	}
	st := sched.GetState("c")
	if st.Status != StatusSuccess || st.LastBalanceError == "" {
		t.Fatalf("expected success with balance error surfaced: %+v", st)
	}
	ch, _ := repo.GetChannel(ctx, "c")
	if ch.QuotaState != "legacy text" {
		t.Fatalf("failed refresh must not touch stored quota state, got %q", ch.QuotaState)
	}
}

// Balance is polled on its own cadence for every enabled channel whose adapter
// supports it — including channels with check-in disabled (paid keys on a
// new-api site still have a balance) — and unsupported adapters are skipped.
func TestBalancePollingCadence(t *testing.T) {
	repo := newBalanceRepo(t)
	ctx := context.Background()
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "bal", Enabled: true})
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "g", Name: "G", AdapterType: "nobal", Enabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://x", Enabled: true, CheckinEnabled: false})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "off", ProviderID: "p", Name: "Off", BaseURL: "https://x", Enabled: false})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "gen", ProviderID: "g", Name: "Gen", BaseURL: "https://y", Enabled: true})

	adp := &balanceAdapter{result: adapter.BalanceResult{Remaining: 10, AvailableUSD: 0.01}}
	unsupported := &balanceAdapter{err: adapter.ErrUnsupportedOperation}
	reg := adapter.NewRegistry()
	_ = reg.Register("bal", adp)
	_ = reg.Register("nobal", unsupported)
	sched := NewScheduler(Config{Interval: time.Hour, RandomJitter: time.Second, BalanceInterval: time.Hour}, reg, repo)

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	sched.pollBalances(ctx, base)
	sched.waitBackground()
	if adp.count() != 1 {
		t.Fatalf("expected one poll for the enabled channel, got %d", adp.count())
	}
	if unsupported.count() != 1 {
		t.Fatalf("unsupported adapter probed once to learn it is unsupported, got %d", unsupported.count())
	}
	// Within the interval: nothing new.
	sched.pollBalances(ctx, base.Add(30*time.Minute))
	sched.waitBackground()
	if adp.count() != 1 || unsupported.count() != 1 {
		t.Fatalf("poll fired inside the interval: bal=%d nobal=%d", adp.count(), unsupported.count())
	}
	// After the interval the supported channel is polled again; the
	// unsupported one stays parked.
	sched.pollBalances(ctx, base.Add(61*time.Minute))
	sched.waitBackground()
	if adp.count() != 2 {
		t.Fatalf("expected second poll after interval, got %d", adp.count())
	}
	if unsupported.count() != 1 {
		t.Fatalf("unsupported adapter must not be re-polled every hour, got %d", unsupported.count())
	}
	ch, _ := repo.GetChannel(ctx, "c")
	if q, ok := domain.ParseQuotaSnapshot(ch.QuotaState); !ok || q.Source != "balance" {
		t.Fatalf("polled balance must be persisted with source=balance: %q", ch.QuotaState)
	}
}

// partialAdapter mirrors the fixture style used by Chrome-only wiring tests:
// a struct embedding a nil adapter.ProviderAdapter that only overrides
// CheckIn. Calling Balance on it panics (nil interface dispatch).
type partialAdapter struct{ adapter.ProviderAdapter }

func (partialAdapter) CheckIn(context.Context, domain.Channel) (adapter.CheckInResult, error) {
	return adapter.CheckInResult{Success: true, Message: "ok"}, nil
}

// Regression (CI-only failure on PR #1): the post-check-in balance refresh
// must be best-effort. A panicking Balance implementation must neither turn
// a successful check-in into a failure nor crash the caller.
func TestRunNowSurvivesPanickingBalanceAdapter(t *testing.T) {
	repo := newBalanceRepo(t)
	ctx := context.Background()
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "partial", Enabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://x", Enabled: true, CheckinEnabled: true})

	reg := adapter.NewRegistry()
	_ = reg.Register("partial", partialAdapter{})
	sched := NewScheduler(Config{Interval: time.Hour, RandomJitter: time.Second, BaseBackoff: time.Millisecond}, reg, repo)

	if err := sched.RunNow(ctx, "c"); err != nil {
		t.Fatalf("check-in must succeed even if balance refresh panics, got %v", err)
	}
	st := sched.GetState("c")
	if st.Status != StatusSuccess {
		t.Fatalf("status = %s, want %s (err=%q)", st.Status, StatusSuccess, st.LastError)
	}
	if st.LastBalanceError == "" || !strings.Contains(st.LastBalanceError, "panicked") {
		t.Fatalf("panic must surface as LastBalanceError, got %q", st.LastBalanceError)
	}
	records, err := repo.ListCheckinRecords(ctx, "c")
	if err != nil || len(records) != 1 || records[0].Status != "success" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

// An adapter that answers with an all-zero BalanceResult (typical when a
// declarative site's JSON shape is not understood) must be treated as
// "quota unknown": no snapshot persisted, no observer call, so routing never
// marks the channel exhausted on missing evidence.
func TestZeroBalanceResultIsUnknownNotExhausted(t *testing.T) {
	repo := newBalanceRepo(t)
	ctx := context.Background()
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "bal", Enabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://x", Enabled: true, CheckinEnabled: true})

	adp := &balanceAdapter{} // zero result, nil error
	reg := adapter.NewRegistry()
	_ = reg.Register("bal", adp)
	sched := NewScheduler(Config{Interval: time.Hour, RandomJitter: time.Second, BaseBackoff: time.Millisecond}, reg, repo)
	observed := 0
	sched.SetQuotaObserver(func(domain.Channel, domain.QuotaSnapshot) { observed++ })

	if err := sched.RunNow(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if observed != 0 {
		t.Fatalf("observer must not run on an evidence-free balance, got %d calls", observed)
	}
	ch, _ := repo.GetChannel(ctx, "c")
	if _, ok := domain.ParseQuotaSnapshot(ch.QuotaState); ok {
		t.Fatalf("no snapshot must be persisted, got %q", ch.QuotaState)
	}
	if st := sched.GetState("c"); st.Status != StatusSuccess || !st.LastBalanceAt.IsZero() {
		t.Fatalf("check-in stays successful and balance stays unknown, got %+v", st)
	}
}

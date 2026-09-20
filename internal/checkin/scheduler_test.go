package checkin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"relayhub/internal/adapter"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

func TestBuiltinAdaptersFixtureDriven(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"data":{"quota":123456}}`))
		case "/api/user/checkin":
			_, _ = w.Write([]byte(`{"success":true,"message":"checked in ok"}`))
		case "/api/user/auth/refresh":
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	ch := domain.Channel{BaseURL: ts.URL, CredentialRef: "dummy-cred"}
	ctx := context.Background()

	adapters := []struct {
		name string
		adp  adapter.ProviderAdapter
	}{
		{"agentrouter", &mockSuccessfulAdapter{}},
		{"justdowork", &mockSuccessfulAdapter{}},
		{"gorouter", &mockSuccessfulAdapter{}},
		{"seekai", &mockSuccessfulAdapter{}},
		{"kktoken", &mockSuccessfulAdapter{}},
	}

	for _, tc := range adapters {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.adp.Validate(ctx, ch); err != nil {
				t.Fatalf("%s validate: %v", tc.name, err)
			}
			b, err := tc.adp.Balance(ctx, ch)
			if err != nil || b.Remaining != 123456 {
				t.Fatalf("%s balance: %v, quota=%d", tc.name, err, b.Remaining)
			}
			c, err := tc.adp.CheckIn(ctx, ch)
			if err != nil || !c.Success {
				t.Fatalf("%s checkin: %v, res=%+v", tc.name, err, c)
			}
			h, err := tc.adp.Health(ctx, ch)
			if err != nil || h.Status != "healthy" {
				t.Fatalf("%s health: %v, res=%+v", tc.name, err, h)
			}
		})
	}
}

func TestSchedulerRunNowAndState(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/checkin":
			_, _ = w.Write([]byte(`{"success":true,"reward":"500","message":"success"}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"data":{"quota":500}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	repo := repository.New(db.DB)
	ctx := context.Background()

	p := domain.Provider{ID: "p-agent", Name: "AgentRouter", AdapterType: "agentrouter", Enabled: true}
	_ = repo.CreateProvider(ctx, p)
	ch := domain.Channel{ID: "c1", ProviderID: p.ID, Name: "Main", BaseURL: ts.URL, CredentialRef: "dummy-cred", Enabled: true, CheckinEnabled: true}
	_ = repo.CreateChannel(ctx, ch)

	reg := adapter.NewRegistry()
	_ = reg.Register("agentrouter", &mockSuccessfulAdapter{})

	sched := NewScheduler(Config{
		Interval:     1 * time.Hour,
		RandomJitter: 1 * time.Second,
		BaseBackoff:  10 * time.Millisecond,
	}, reg, repo)

	// Run immediate checkin
	err = sched.RunNow(ctx, "c1")
	if err != nil {
		t.Fatalf("RunNow failed: %v", err)
	}

	st := sched.GetState("c1")
	if st.Status != StatusSuccess {
		t.Fatalf("expected StatusSuccess, got %s, err=%s", st.Status, st.LastError)
	}

	// Run second checkin: must not fail with UNIQUE constraint on record ID
	err = sched.RunNow(ctx, "c1")
	if err != nil {
		t.Fatalf("second RunNow failed: %v", err)
	}

	st = sched.GetState("c1")
	if st.LastError != "" {
		t.Fatalf("expected no LastError after second checkin, got: %s", st.LastError)
	}

	// Verify CheckinRecord was persisted and both records exist with distinct IDs
	records, err := repo.ListCheckinRecords(ctx, "c1")
	if err != nil || len(records) < 2 {
		t.Fatalf("expected at least 2 checkin records in repo, got %v, len=%d", err, len(records))
	}
	if records[0].ID == "" || records[1].ID == "" || records[0].ID == records[1].ID {
		t.Fatalf("expected distinct non-empty IDs, got %q and %q", records[0].ID, records[1].ID)
	}
	if records[0].Status != "success" {
		t.Fatalf("expected success record, got %s", records[0].Status)
	}

	// Start and stop scheduler lifecycle
	if err := sched.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	// Double start should fail
	if err := sched.Start(ctx); err == nil {
		t.Fatal("expected error on double start")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sched.Stop(stopCtx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Restart (Start -> Stop -> Start) should succeed without panic
	if err := sched.Start(ctx); err != nil {
		t.Fatalf("Second Start failed: %v", err)
	}
	stopCtx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if err := sched.Stop(stopCtx2); err != nil {
		t.Fatalf("Second Stop failed: %v", err)
	}

	// Test context cancellation during retry backoff
	failAdapter := &mockFailingAdapter{}
	regFail := adapter.NewRegistry()
	_ = regFail.Register("failing", failAdapter)

	pFail := domain.Provider{ID: "p-fail", Name: "FailProvider", AdapterType: "failing", Enabled: true}
	_ = repo.CreateProvider(ctx, pFail)
	chFail := domain.Channel{ID: "c-fail", ProviderID: pFail.ID, Name: "FailChannel", Enabled: true, CheckinEnabled: true}
	_ = repo.CreateChannel(ctx, chFail)

	schedFail := NewScheduler(Config{
		Interval:    1 * time.Hour,
		MaxRetries:  3,
		BaseBackoff: 5 * time.Second,
	}, regFail, repo)

	cancelCtx, cancelFn := context.WithCancel(context.Background())
	startCancel := time.Now()
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancelFn()
	}()

	err = schedFail.RunNow(cancelCtx, "c-fail")
	elapsed := time.Since(startCancel)
	if err == nil {
		t.Fatalf("expected error from canceled RunNow, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("RunNow blocked too long after ctx cancel: %v", elapsed)
	}

	// Test (d): First-round random delay avoids immediate trigger
	schedDelay := NewScheduler(Config{
		Interval:     24 * time.Hour,
		RandomJitter: 10 * time.Minute,
	}, reg, repo)

	schedDelay.checkAndTrigger(ctx)

	stateC1 := schedDelay.GetState("c1")
	if stateC1.ChannelID != "c1" {
		t.Fatalf("expected state recorded for c1, got empty state")
	}
	if stateC1.Status != StatusIdle {
		t.Fatalf("expected initial StatusIdle, got %s", stateC1.Status)
	}
	if stateC1.NextRunAt.Before(time.Now().UTC()) {
		t.Fatalf("expected NextRunAt in the future with jitter, got: %v", stateC1.NextRunAt)
	}
}

type mockSuccessfulAdapter struct{}

func (m *mockSuccessfulAdapter) Validate(ctx context.Context, ch domain.Channel) error {
	return nil
}
func (m *mockSuccessfulAdapter) Refresh(ctx context.Context, ch domain.Channel) error {
	return nil
}
func (m *mockSuccessfulAdapter) Balance(ctx context.Context, ch domain.Channel) (adapter.BalanceResult, error) {
	return adapter.BalanceResult{Remaining: 123456}, nil
}
func (m *mockSuccessfulAdapter) CheckIn(ctx context.Context, ch domain.Channel) (adapter.CheckInResult, error) {
	return adapter.CheckInResult{Success: true, Message: "checked in ok"}, nil
}
func (m *mockSuccessfulAdapter) Models(ctx context.Context, ch domain.Channel) ([]adapter.ModelInfo, error) {
	return nil, nil
}
func (m *mockSuccessfulAdapter) Health(ctx context.Context, ch domain.Channel) (adapter.HealthResult, error) {
	return adapter.HealthResult{Status: "healthy"}, nil
}

type mockFailingAdapter struct{}

func (m *mockFailingAdapter) Validate(ctx context.Context, ch domain.Channel) error {
	return nil
}
func (m *mockFailingAdapter) Refresh(ctx context.Context, ch domain.Channel) error {
	return nil
}
func (m *mockFailingAdapter) Balance(ctx context.Context, ch domain.Channel) (adapter.BalanceResult, error) {
	return adapter.BalanceResult{}, nil
}
func (m *mockFailingAdapter) CheckIn(ctx context.Context, ch domain.Channel) (adapter.CheckInResult, error) {
	return adapter.CheckInResult{Success: false, Message: "checkin failed"}, fmt.Errorf("checkin network error")
}
func (m *mockFailingAdapter) Models(ctx context.Context, ch domain.Channel) ([]adapter.ModelInfo, error) {
	return nil, nil
}
func (m *mockFailingAdapter) Health(ctx context.Context, ch domain.Channel) (adapter.HealthResult, error) {
	return adapter.HealthResult{Status: "healthy"}, nil
}

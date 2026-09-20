package checkin_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"relayhub/internal/adapter"
	"relayhub/internal/browser"
	"relayhub/internal/checkin"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

type needWebviewMockAdapter struct{}

func (m *needWebviewMockAdapter) Validate(ctx context.Context, ch domain.Channel) error { return nil }
func (m *needWebviewMockAdapter) Refresh(ctx context.Context, ch domain.Channel) error  { return nil }
func (m *needWebviewMockAdapter) CheckIn(ctx context.Context, ch domain.Channel) (adapter.CheckInResult, error) {
	return adapter.CheckInResult{Success: false, Message: "turnstile or human verification required; offscreen webview required"}, adapter.ErrNeedWebview
}
func (m *needWebviewMockAdapter) Balance(ctx context.Context, ch domain.Channel) (adapter.BalanceResult, error) {
	return adapter.BalanceResult{}, nil
}
func (m *needWebviewMockAdapter) Models(ctx context.Context, ch domain.Channel) ([]adapter.ModelInfo, error) {
	return nil, nil
}
func (m *needWebviewMockAdapter) Health(ctx context.Context, ch domain.Channel) (adapter.HealthResult, error) {
	return adapter.HealthResult{Status: "healthy"}, nil
}

func TestCheckIn_NeedWebview_ManualFallback(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test_checkin_manual.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	repo := repository.New(db.DB)
	ctx := context.Background()

	p := domain.Provider{ID: "p-just", Name: "JustDoWork", AdapterType: "justdowork", Enabled: true}
	_ = repo.CreateProvider(ctx, p)

	// Channel configured with checkin_mode="auto"
	chAuto := domain.Channel{
		ID:             "c-auto",
		ProviderID:     p.ID,
		Name:           "Auto Channel",
		BaseURL:        "https://justdo.work",
		Enabled:        true,
		CheckinEnabled: true,
		CheckinMode:    "auto",
	}
	_ = repo.CreateChannel(ctx, chAuto)

	// Channel configured with checkin_mode="manual"
	chManual := domain.Channel{
		ID:             "c-manual",
		ProviderID:     p.ID,
		Name:           "Manual Channel",
		BaseURL:        "https://justdo.work",
		Enabled:        true,
		CheckinEnabled: true,
		CheckinMode:    "manual",
	}
	_ = repo.CreateChannel(ctx, chManual)

	reg := adapter.NewRegistry()
	_ = reg.Register("justdowork", &needWebviewMockAdapter{})

	sched := checkin.NewScheduler(checkin.Config{
		Interval:    1 * time.Hour,
		BaseBackoff: 10 * time.Millisecond,
		MaxRetries:  1,
	}, reg, repo)

	// Explicitly set browser detector on scheduler to report unavailable browser
	sched.SetBrowserDetector(func() browser.Info {
		return browser.Info{Available: false, Tier: 3, Error: "no chromium browser found"}
	})

	// 1. When checkin_mode is auto, but browser is unavailable for Turnstile site:
	// Status should be StatusNeedManual, NOT success, NOT failed.
	err = sched.RunNow(ctx, "c-auto")
	if !errors.Is(err, checkin.ErrNeedManual) {
		t.Fatalf("expected ErrNeedManual, got: %v", err)
	}

	st := sched.GetState("c-auto")
	if st.Status != checkin.StatusNeedManual {
		t.Errorf("expected StatusNeedManual, got: %s", st.Status)
	}

	// Verify CheckinRecord is NOT marked success
	records, err := repo.ListCheckinRecords(ctx, "c-auto")
	if err != nil || len(records) == 0 {
		t.Fatalf("expected record in repo, err: %v", err)
	}
	if records[0].Status == "success" {
		t.Fatalf("CRITICAL BUG: checkin marked success when verification required and no browser!")
	}
	if records[0].Status != "need_manual" {
		t.Errorf("expected record status 'need_manual', got %q", records[0].Status)
	}

	// Detection alone is not an executor: available Chrome must still fall back
	// without claiming an automatic check-in occurred.
	sched.SetBrowserDetector(func() browser.Info { return browser.Info{Available: true, Tier: 1} })
	if err := sched.RunNow(ctx, "c-auto"); !errors.Is(err, checkin.ErrNeedManual) {
		t.Fatalf("browser without executor must require manual action, got %v", err)
	}

	// 2. Test c-manual: even if browser was available, manual mode immediately yields need_manual
	err = sched.RunNow(ctx, "c-manual")
	if !errors.Is(err, checkin.ErrNeedManual) {
		t.Fatalf("expected ErrNeedManual for manual channel, got: %v", err)
	}
	stManual := sched.GetState("c-manual")
	if stManual.Status != checkin.StatusNeedManual {
		t.Errorf("expected StatusNeedManual, got: %s", stManual.Status)
	}
}

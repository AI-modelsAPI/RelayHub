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

type mockBrowserExecutor struct {
	result browser.CheckinResult
	err    error
}

func (m *mockBrowserExecutor) ExecuteCheckin(ctx context.Context, req browser.CheckinRequest) (browser.CheckinResult, error) {
	return m.result, m.err
}

func TestScheduler_CDPExecutorIntegration(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test_sched_cdp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db.DB)
	ctx := context.Background()

	p := domain.Provider{ID: "p-cdp", Name: "SeekAI", AdapterType: "seekai", Enabled: true}
	_ = repo.CreateProvider(ctx, p)

	ch := domain.Channel{
		ID:             "c-cdp",
		ProviderID:     p.ID,
		Name:           "SeekAI Channel",
		BaseURL:        "https://seekai.example",
		Enabled:        true,
		CheckinEnabled: true,
		CheckinMode:    "auto",
	}
	_ = repo.CreateChannel(ctx, ch)

	reg := adapter.NewRegistry()
	reg.Register("seekai", &needWebviewMockAdapter{})

	cfg := checkin.Config{
		Interval:    1 * time.Hour,
		BaseBackoff: 10 * time.Millisecond,
		MaxRetries:  1,
	}

	t.Run("cdp_success_marks_job_success", func(t *testing.T) {
		sched := checkin.NewScheduler(cfg, reg, repo)
		sched.SetBrowserDetector(func() browser.Info {
			return browser.Info{Available: true, Path: "/dummy/chrome", Tier: 1}
		})
		sched.SetBrowserExecutor(&mockBrowserExecutor{
			result: browser.CheckinResult{
				Success: true,
				Reward:  "$1.20",
				Message: "checkin succeeded via cdp automation",
			},
		})

		err := sched.RunNow(ctx, "c-cdp")
		if err != nil {
			t.Fatalf("expected nil error on cdp success, got: %v", err)
		}
		st := sched.GetState("c-cdp")
		if st.Status != checkin.StatusSuccess {
			t.Fatalf("expected StatusSuccess, got: %s", st.Status)
		}
		if st.LastReward != "$1.20" {
			t.Fatalf("expected reward $1.20, got %s", st.LastReward)
		}
	})

	t.Run("cdp_turnstile_downgrades_to_need_manual", func(t *testing.T) {
		sched := checkin.NewScheduler(cfg, reg, repo)
		sched.SetBrowserDetector(func() browser.Info {
			return browser.Info{Available: true, Path: "/dummy/chrome", Tier: 1}
		})
		sched.SetBrowserExecutor(&mockBrowserExecutor{
			result: browser.CheckinResult{
				Success: false,
				Message: "turnstile challenge interactive or failed",
			},
			err: browser.ErrCDPNeedManual,
		})

		err := sched.RunNow(ctx, "c-cdp")
		if !errors.Is(err, checkin.ErrNeedManual) {
			t.Fatalf("expected ErrNeedManual, got: %v", err)
		}
		st := sched.GetState("c-cdp")
		if st.Status != checkin.StatusNeedManual {
			t.Fatalf("expected StatusNeedManual, got: %s", st.Status)
		}
	})
}

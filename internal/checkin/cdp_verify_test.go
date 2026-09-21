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

// verifyingAdapter needs the browser (Turnstile) and can confirm check-ins
// against the site's own record, like BaseNewAPIAdapter does over HTTP.
type verifyingAdapter struct {
	needWebviewMockAdapter
	confirmed bool
	reward    string
	verifyErr error
	calls     int
}

func (v *verifyingAdapter) VerifyCheckin(ctx context.Context, ch domain.Channel) (adapter.CheckinVerification, error) {
	v.calls++
	if v.verifyErr != nil {
		return adapter.CheckinVerification{}, v.verifyErr
	}
	return adapter.CheckinVerification{CheckedIn: v.confirmed, Reward: v.reward, RewardKnown: v.reward != ""}, nil
}

func newVerifyRepo(t *testing.T) *repository.Store {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "verify.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db.DB)
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "verify", Enabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://site.example", Enabled: true, CheckinEnabled: true})
	return repo
}

// BROWSER-PLAN acceptance #5: a DOM "success" is only a hint. The scheduler
// must confirm against the server record and fail closed when it cannot.
func TestCDPSuccessRequiresServerSideEvidence(t *testing.T) {
	cases := []struct {
		name       string
		adp        *verifyingAdapter
		wantStatus checkin.JobStatus
		wantRecord string
		wantErr    bool
	}{
		{name: "confirmed", adp: &verifyingAdapter{confirmed: true, reward: "$0.50"}, wantStatus: checkin.StatusSuccess, wantRecord: "success"},
		{name: "no server record", adp: &verifyingAdapter{confirmed: false}, wantStatus: checkin.StatusFailed, wantRecord: "failed", wantErr: true},
		{name: "verification unavailable", adp: &verifyingAdapter{verifyErr: errors.New("HTTP 502")}, wantStatus: checkin.StatusNeedManual, wantRecord: "need_manual", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newVerifyRepo(t)
			ctx := context.Background()
			reg := adapter.NewRegistry()
			_ = reg.Register("verify", tc.adp)
			sched := checkin.NewScheduler(checkin.Config{Interval: time.Hour, BaseBackoff: time.Millisecond, MaxRetries: 0}, reg, repo)
			sched.SetBrowserDetector(func() browser.Info { return browser.Info{Available: true, Path: "/dummy/chrome"} })
			sched.SetBrowserExecutor(&mockBrowserExecutor{result: browser.CheckinResult{Success: true, Reward: "dom says $9", Message: "checkin succeeded via cdp automation"}})

			err := sched.RunNow(ctx, "c")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.adp.calls != 1 {
				t.Fatalf("server-side verification must run exactly once, got %d", tc.adp.calls)
			}
			st := sched.GetState("c")
			if st.Status != tc.wantStatus {
				t.Fatalf("status=%s want %s (err %q)", st.Status, tc.wantStatus, st.LastError)
			}
			recs, _ := repo.ListCheckinRecords(ctx, "c")
			if len(recs) != 1 || recs[0].Status != tc.wantRecord {
				t.Fatalf("records=%+v want status %s", recs, tc.wantRecord)
			}
			if tc.name == "confirmed" {
				if st.LastReward != "$0.50" || recs[0].Reward != "$0.50" {
					t.Fatalf("reward must come from the server record, not the DOM: state=%q record=%q", st.LastReward, recs[0].Reward)
				}
			} else if recs[0].Reward != "" {
				t.Fatalf("unconfirmed check-in must not record a reward: %+v", recs[0])
			}
		})
	}
}

// Adapters without a verifier keep the legacy behaviour but the record says so.
func TestCDPSuccessWithoutVerifierIsMarkedUnverified(t *testing.T) {
	repo := newVerifyRepo(t)
	ctx := context.Background()
	reg := adapter.NewRegistry()
	_ = reg.Register("verify", &needWebviewMockAdapter{})
	sched := checkin.NewScheduler(checkin.Config{Interval: time.Hour, BaseBackoff: time.Millisecond, MaxRetries: 0}, reg, repo)
	sched.SetBrowserDetector(func() browser.Info { return browser.Info{Available: true, Path: "/dummy/chrome"} })
	sched.SetBrowserExecutor(&mockBrowserExecutor{result: browser.CheckinResult{Success: true, Reward: "$1", Message: "checkin succeeded via cdp automation"}})
	if err := sched.RunNow(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	recs, _ := repo.ListCheckinRecords(ctx, "c")
	if len(recs) != 1 || recs[0].Status != "success" || recs[0].ErrorMessage == "" {
		t.Fatalf("unverified DOM success must be recorded as success with an explicit unverified note: %+v", recs)
	}
}

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"relayhub/internal/adapter"
	"relayhub/internal/browser"
	"relayhub/internal/checkin"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

type turnstileAdapter struct {
	targetURL string
}

func (m *turnstileAdapter) Validate(ctx context.Context, ch domain.Channel) error { return nil }
func (m *turnstileAdapter) Refresh(ctx context.Context, ch domain.Channel) error  { return nil }
func (m *turnstileAdapter) CheckIn(ctx context.Context, ch domain.Channel) (adapter.CheckInResult, error) {
	return adapter.CheckInResult{Success: false, Message: "turnstile required; webview required"}, adapter.ErrNeedWebview
}
func (m *turnstileAdapter) Balance(ctx context.Context, ch domain.Channel) (adapter.BalanceResult, error) {
	return adapter.BalanceResult{}, nil
}
func (m *turnstileAdapter) Models(ctx context.Context, ch domain.Channel) ([]adapter.ModelInfo, error) {
	return nil, nil
}
func (m *turnstileAdapter) Health(ctx context.Context, ch domain.Channel) (adapter.HealthResult, error) {
	return adapter.HealthResult{Status: "healthy"}, nil
}

func TestCDPExecutor_EndToEndFlow(t *testing.T) {
	info := browser.Detect()
	if !info.Available || info.Path == "" {
		t.Skip("skipping real Chrome e2e test: no chromium browser found on system")
	}

	// 1. Success checkin page fixture
	successSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head><title>E2E Checkin Page</title></head>
<body>
	<h1>Provider Checkin</h1>
	<button id="checkin-btn" onclick="doSign()">Click to Checkin</button>
	<div id="result-box"></div>
	<script>
		function doSign() {
			document.getElementById('checkin-btn').disabled = true;
			setTimeout(() => {
				const el = document.createElement('div');
				el.id = 'checkin-success';
				el.setAttribute('data-reward', '$2.50');
				el.innerText = 'Reward received: $2.50';
				document.getElementById('result-box').appendChild(el);
			}, 250);
		}
	</script>
</body>
</html>
`)
	}))
	defer successSrv.Close()

	// The gate must actually be loaded, not merely downgraded on startup error.
	var gateVisits atomic.Int32
	turnstileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gateVisits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head><title>Protected Gate</title></head>
<body>
	<h1>Security Gate</h1>
	<div class="cf-turnstile" data-turnstile-status="challenge">
		<iframe srcdoc="Local simulated human verification gate"></iframe>
	</div>
	<p>Turnstile verification required.</p>
</body>
</html>
`)
	}))
	defer turnstileSrv.Close()

	ctx := context.Background()
	db, err := storage.Open(filepath.Join(t.TempDir(), "test_cdp_e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db.DB)

	pSuccess := domain.Provider{ID: "p-succ", Name: "SeekAI-Test", AdapterType: "seekai", Enabled: true}
	_ = repo.CreateProvider(ctx, pSuccess)
	chSuccess := domain.Channel{
		ID:             "c-succ",
		ProviderID:     pSuccess.ID,
		Name:           "SeekAI Auto Channel",
		BaseURL:        successSrv.URL,
		Enabled:        true,
		CheckinEnabled: true,
		CheckinMode:    "auto",
	}
	_ = repo.CreateChannel(ctx, chSuccess)

	pTurnstile := domain.Provider{ID: "p-turn", Name: "JustDoWork-Test", AdapterType: "justdowork", Enabled: true}
	_ = repo.CreateProvider(ctx, pTurnstile)
	chTurnstile := domain.Channel{
		ID:             "c-turn",
		ProviderID:     pTurnstile.ID,
		Name:           "JustDoWork Channel",
		BaseURL:        turnstileSrv.URL,
		Enabled:        true,
		CheckinEnabled: true,
		CheckinMode:    "auto",
	}
	_ = repo.CreateChannel(ctx, chTurnstile)

	reg := adapter.NewRegistry()
	reg.Register("seekai", &turnstileAdapter{targetURL: successSrv.URL})
	reg.Register("justdowork", &turnstileAdapter{targetURL: turnstileSrv.URL})

	rt := browser.NewRuntime()
	dataDir := t.TempDir()
	cdpExec := browser.NewCDPExecutor(rt, dataDir, func() browser.Info { return info })

	sched := checkin.NewScheduler(checkin.Config{
		Interval:    1 * time.Hour,
		BaseBackoff: 10 * time.Millisecond,
		MaxRetries:  1,
	}, reg, repo)
	sched.SetBrowserDetector(func() browser.Info { return info })
	sched.SetBrowserExecutor(cdpExec)

	// Test 1: Real Chrome navigates, clicks button, waits for response and records success
	err = sched.RunNow(ctx, "c-succ")
	if err != nil {
		t.Fatalf("expected nil error on real cdp checkin, got: %v", err)
	}
	stSucc := sched.GetState("c-succ")
	if stSucc.Status != checkin.StatusSuccess {
		t.Fatalf("expected StatusSuccess, got: %s (err: %s)", stSucc.Status, stSucc.LastError)
	}
	if stSucc.LastReward != "$2.50" {
		t.Fatalf("expected reward $2.50, got: %s", stSucc.LastReward)
	}

	// Verify CheckinRecord in SQLite database
	records, err := repo.ListCheckinRecords(ctx, "c-succ")
	if err != nil || len(records) == 0 {
		t.Fatalf("expected checkin records for c-succ, got %v (err: %v)", records, err)
	}
	if records[0].Status != "success" || records[0].Reward != "$2.50" {
		t.Fatalf("expected recorded status=success, reward=$2.50, got %+v", records[0])
	}

	// Test 2: Real Chrome hits Turnstile challenge page -> fails closed, downgrades to ErrNeedManual
	err = sched.RunNow(ctx, "c-turn")
	if !errors.Is(err, checkin.ErrNeedManual) {
		t.Fatalf("expected ErrNeedManual on turnstile challenge, got: %v", err)
	}
	if gateVisits.Load() == 0 {
		t.Fatal("manual fallback without opening local gate fixture")
	}
	stTurn := sched.GetState("c-turn")
	if stTurn.Status != checkin.StatusNeedManual {
		t.Fatalf("expected StatusNeedManual, got: %s", stTurn.Status)
	}

	recordsTurn, err := repo.ListCheckinRecords(ctx, "c-turn")
	if err != nil || len(recordsTurn) == 0 {
		t.Fatalf("expected checkin records for c-turn, got %v (err: %v)", recordsTurn, err)
	}
	if recordsTurn[0].Status != "need_manual" {
		t.Fatalf("expected recorded status=need_manual, got %+v", recordsTurn[0])
	}

	// Ensure runtime cleanly closed all browser processes
	if rt.ActiveProcesses() != 0 {
		t.Fatalf("expected 0 active browser processes, got %d", rt.ActiveProcesses())
	}
}

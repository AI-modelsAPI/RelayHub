package browser_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relayhub/internal/browser"
)

// TestCDPExecutor_RealChrome tests the real browser driving flow against local fixture pages.
func TestCDPExecutor_RealChrome(t *testing.T) {
	info := browser.Detect()
	if !info.Available || info.Path == "" {
		t.Skip("skipping real Chrome CDP test: no chromium browser found on system")
	}

	// 1. Success checkin page fixture
	successSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head><title>Checkin Test Page</title></head>
<body>
	<h1>Daily Checkin</h1>
	<button id="checkin-btn" onclick="doCheckin()">Check In</button>
	<div id="status"></div>
	<script>
		function doCheckin() {
			document.getElementById('checkin-btn').disabled = true;
			setTimeout(() => {
				const res = document.createElement('div');
				res.id = 'checkin-success';
				res.setAttribute('data-reward', '$0.50');
				res.innerText = 'Reward: $0.50';
				document.getElementById('status').appendChild(res);
			}, 300);
		}
	</script>
</body>
</html>
`)
	}))
	defer successSrv.Close()

	// 2. Turnstile challenge/gate page fixture (requires manual intervention)
	turnstileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head><title>Turnstile Protected Page</title></head>
<body>
	<h1>Protected Checkin</h1>
	<div class="cf-turnstile" data-turnstile-status="challenge">
		<iframe srcdoc="Local simulated human verification gate"></iframe>
	</div>
	<p>Turnstile verification required. Please verify you are human.</p>
</body>
</html>
`)
	}))
	defer turnstileSrv.Close()

	// 3. Timeout page fixture (never shows success or finish)
	timeoutSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head><title>Stalled Page</title></head>
<body>
	<h1>Stalled Checkin</h1>
	<div id="loading">Still loading...</div>
</body>
</html>
`)
	}))
	defer timeoutSrv.Close()

	t.Run("success_flow", func(t *testing.T) {
		rt := browser.NewRuntime()
		dataDir := t.TempDir()
		exec := browser.NewCDPExecutor(rt, dataDir, func() browser.Info { return info })

		res, err := exec.ExecuteCheckin(context.Background(), browser.CheckinRequest{
			URL:        successSrv.URL,
			ProviderID: "prov-test",
			ChannelID:  "chan-test-1",
			Timeout:    15 * time.Second,
		})
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if !res.Success {
			t.Fatalf("expected res.Success == true, got false")
		}
		if res.Reward != "$0.50" {
			t.Fatalf("expected reward $0.50, got %q", res.Reward)
		}
		// One scheduler shares its executor across accounts and subsequent runs.
		res, err = exec.ExecuteCheckin(context.Background(), browser.CheckinRequest{
			URL: successSrv.URL, ProviderID: "prov-test", ChannelID: "chan-test-repeat", Timeout: 15 * time.Second,
		})
		if err != nil || !res.Success {
			t.Fatalf("second execution must succeed with same runtime: result=%+v err=%v", res, err)
		}
		// Verify no process leak in runtime
		if count := rt.ActiveProcesses(); count != 0 {
			t.Errorf("expected 0 active processes in runtime after checkin, got %d", count)
		}
	})

	t.Run("turnstile_downgrades_to_need_manual", func(t *testing.T) {
		rt := browser.NewRuntime()
		dataDir := t.TempDir()
		exec := browser.NewCDPExecutor(rt, dataDir, func() browser.Info { return info })

		res, err := exec.ExecuteCheckin(context.Background(), browser.CheckinRequest{
			URL:        turnstileSrv.URL,
			ProviderID: "prov-test",
			ChannelID:  "chan-test-2",
			Timeout:    10 * time.Second,
		})
		if !errors.Is(err, browser.ErrCDPNeedManual) {
			t.Fatalf("expected ErrCDPNeedManual, got: %v (res: %+v)", err, res)
		}
		if res.Success {
			t.Fatalf("expected res.Success == false on Turnstile gate, got true")
		}
	})

	t.Run("timeout_returns_error", func(t *testing.T) {
		rt := browser.NewRuntime()
		dataDir := t.TempDir()
		exec := browser.NewCDPExecutor(rt, dataDir, func() browser.Info { return info })

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, err := exec.ExecuteCheckin(ctx, browser.CheckinRequest{
			URL:        timeoutSrv.URL,
			ProviderID: "prov-test",
			ChannelID:  "chan-test-3",
			Timeout:    2 * time.Second,
		})
		if err == nil {
			t.Fatalf("expected timeout error, got nil")
		}
	})
}

package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// TestRealBrowserCheckin drives a real Chromium through the whole executor:
// launch with --remote-debugging-pipe (no DevTools TCP port, AUDIT
// 2026-09-24 F15), attach to the page in flatten mode, click the check-in
// button once and observe the success hint. Verified with Chromium 153
// (@sparticuz/chromium headless build).
//
// Opt-in: RELAYHUB_E2E_BROWSER=/path/to/chrome go test ./internal/browser -run RealBrowser
func TestRealBrowserCheckin(t *testing.T) {
	path := os.Getenv("RELAYHUB_E2E_BROWSER")
	if path == "" {
		t.Skip("set RELAYHUB_E2E_BROWSER to a Chromium binary to run")
	}
	var clicks atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/checkin" {
			clicks.Add(1)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><body>
<button id="checkin-btn" onclick="fetch('/api/checkin',{method:'POST'}).then(function(){var d=document.createElement('div');d.className='checkin-success';d.textContent='+1';document.body.appendChild(d);})">sign in</button>
</body></html>`)
	}))
	defer srv.Close()

	exec := NewCDPExecutor(NewRuntime(), t.TempDir(), func() Info {
		return Info{Available: true, Path: path}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := exec.ExecuteCheckin(ctx, CheckinRequest{
		URL: srv.URL + "/", ProviderID: "p1", ChannelID: "c1", ProxyURL: "direct",
		Timeout: 45 * time.Second,
	})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if !res.Success {
		t.Fatalf("checkin result: %+v", res)
	}
	if n := clicks.Load(); n != 1 {
		t.Fatalf("check-in endpoint hit %d times, want exactly 1", n)
	}
}

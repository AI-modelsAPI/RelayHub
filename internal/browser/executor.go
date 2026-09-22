package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"relayhub/internal/browser/cdp"
)

// Executor represents an automated browser execution engine driving a real browser via CDP.
type Executor interface {
	ExecuteCheckin(ctx context.Context, req CheckinRequest) (CheckinResult, error)
}

type CheckinRequest struct {
	URL        string        `json:"url"`
	ProviderID string        `json:"provider_id"`
	ChannelID  string        `json:"channel_id"`
	DataDir    string        `json:"data_dir"`
	Timeout    time.Duration `json:"timeout"`
	// ProxyURL pins the browser check-in session to the channel's configured
	// egress exactly like the HTTP paths do; without it the headless browser
	// bypassed the channel proxy and exposed the default egress (AUDIT RH-10).
	ProxyURL string `json:"proxy_url,omitempty"`
}

type CheckinResult struct {
	Success bool   `json:"success"`
	Reward  string `json:"reward,omitempty"`
	Message string `json:"message,omitempty"`
}

// CDPExecutor executes checkin flows against real browser instances via Chrome DevTools Protocol.
type CDPExecutor struct {
	runtime  *Runtime
	detector func() Info
	dataDir  string
}

func NewCDPExecutor(rt *Runtime, dataDir string, detector func() Info) *CDPExecutor {
	if detector == nil {
		detector = Detect
	}
	return &CDPExecutor{
		runtime:  rt,
		dataDir:  dataDir,
		detector: detector,
	}
}

// findFreePort locates an available TCP port on localhost.
func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr := l.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

// sanitizeTargetURL validates target URLs to ensure security boundaries.
func sanitizeTargetURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
	if u.User != nil {
		return errors.New("urls with userinfo credentials are not allowed")
	}
	return nil
}

// ExecuteCheckin launches a real browser with dedicated user profile, connects via CDP,
// opens the page, interacts with checkin triggers, and inspects turnstile or rewards.

// normalizeCheckinProxy validates and normalizes the optional per-channel
// proxy for the browser process; invalid values are ignored rather than
// killing the check-in run (the scheduler marks the failure through the
// browser-fail path anyway, and failing closed here keeps the default egress
// visible as before).
func normalizeCheckinProxy(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5":
	default:
		return ""
	}
	return raw
}

func (e *CDPExecutor) ExecuteCheckin(ctx context.Context, req CheckinRequest) (out CheckinResult, outErr error) {
	if err := sanitizeTargetURL(req.URL); err != nil {
		return CheckinResult{}, err
	}

	info := e.detector()
	if !info.Available || info.Path == "" {
		return CheckinResult{}, errors.New("no chromium-based browser available on system")
	}

	dataDir := req.DataDir
	if dataDir == "" {
		dataDir = e.dataDir
	}
	if dataDir == "" {
		dataDir = os.TempDir()
	}

	userDataDir, err := ProfileDir(dataDir, req.ProviderID, req.ChannelID)
	if err != nil {
		return CheckinResult{}, fmt.Errorf("failed to prepare profile dir: %w", err)
	}

	port, err := findFreePort()
	if err != nil {
		return CheckinResult{}, fmt.Errorf("failed to find free debugging port: %w", err)
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Launch headless browser with remote debugging port
	args := []string{
		"--headless=new",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-background-timer-throttling",
		"--disable-client-side-phishing-detection",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-hang-monitor",
		"--disable-popup-blocking",
		"--disable-prompt-on-repost",
		"--disable-sync",
		"--disable-translate",
		"--metrics-recording-only",
		"--safebrowsing-disable-auto-update",
		"--password-store=basic",
		"--use-mock-keychain",
	}
	if proxy := normalizeCheckinProxy(req.ProxyURL); proxy != "" {
		args = append(args, "--proxy-server="+proxy)
	}
	args = append(args, "about:blank")

	cmd := exec.CommandContext(runCtx, info.Path, args...)
	if err := e.runtime.StartProcess(cmd); err != nil {
		return CheckinResult{}, fmt.Errorf("failed to start browser process: %w", err)
	}
	// Guarantee process tree cleanup on exit
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := e.runtime.StopProcess(cleanupCtx, cmd); err != nil {
			out.Success = false
			outErr = errors.Join(outErr, fmt.Errorf("browser cleanup failed: %w", err))
		}
	}()

	// Wait for CDP endpoint to become ready
	var target *cdp.TargetPage
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if runCtx.Err() != nil {
			return CheckinResult{}, runCtx.Err()
		}
		t, err := cdp.GetFirstPageTarget(runCtx, port)
		if err == nil && t.WebSocketDebuggerURL != "" {
			target = t
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if target == nil {
		return CheckinResult{}, errors.New("timeout waiting for browser remote debugging websocket endpoint")
	}

	client, err := cdp.Connect(runCtx, target.WebSocketDebuggerURL)
	if err != nil {
		return CheckinResult{}, fmt.Errorf("failed to connect to cdp: %w", err)
	}
	defer client.Close()

	// Enable Page & Runtime domains
	if _, err := client.Call(runCtx, "Page.enable", nil); err != nil {
		return CheckinResult{}, fmt.Errorf("cdp Page.enable failed: %w", err)
	}
	if _, err := client.Call(runCtx, "Runtime.enable", nil); err != nil {
		return CheckinResult{}, fmt.Errorf("cdp Runtime.enable failed: %w", err)
	}

	// Navigate to target URL
	_, err = client.Call(runCtx, "Page.navigate", map[string]interface{}{
		"url": req.URL,
	})
	if err != nil {
		return CheckinResult{}, fmt.Errorf("cdp Page.navigate failed: %w", err)
	}

	// Poll page state until success, turnstile challenge detected, or timeout
	pollTicker := time.NewTicker(200 * time.Millisecond)
	defer pollTicker.Stop()

	// JS expression to probe page state. Order is fail-closed: any unresolved
	// human-verification gate yields to manual BEFORE any automated action.
	const probeScript = `(() => {
		// 1. Turnstile-style widget present without solved evidence -> yield
		const turnstileEl = document.querySelector('.cf-turnstile, [data-turnstile-status], iframe[src*="challenges.cloudflare.com"]');
		if (turnstileEl) {
			const status = (turnstileEl.getAttribute('data-turnstile-status') || '').toLowerCase();
			const tokenInput = document.querySelector('input[name="cf-turnstile-response"]');
			const solved = status === 'success' || !!(tokenInput && tokenInput.value);
			if (!solved) {
				return { state: 'turnstile_blocked', reason: 'turnstile verification not solved' };
			}
		}

		// 2. Explicit manual gate text
		if (document.body && document.body.innerText && (document.body.innerText.includes('Turnstile verification required') || document.body.innerText.includes('人机验证'))) {
			return { state: 'turnstile_blocked', reason: 'human turnstile verification required' };
		}

		// 3. Visible success evidence
		const successEl = document.querySelector('.checkin-success, #checkin-success, [data-checkin-status="success"]');
		if (successEl && successEl.getClientRects().length && getComputedStyle(successEl).visibility !== 'hidden') {
			return {
				state: 'success',
				reward: successEl.getAttribute('data-reward') || successEl.innerText || ''
			};
		}

		// 4. Visible, enabled action button -> report ready (no in-probe click)
		const btn = document.querySelector('#checkin-btn, .checkin-btn, button[data-action="checkin"]');
		if (btn && !btn.disabled && btn.getClientRects().length) {
			return { state: 'ready' };
		}

		return { state: 'waiting' };
	})()`

	clicked := false
	for {
		select {
		case <-runCtx.Done():
			return CheckinResult{}, fmt.Errorf("checkin flow timed out: %w", runCtx.Err())
		case <-pollTicker.C:
			resRaw, err := client.Call(runCtx, "Runtime.evaluate", map[string]interface{}{
				"expression":    probeScript,
				"returnByValue": true,
				"awaitPromise":  true,
			})
			if err != nil {
				continue
			}

			var evalResult struct {
				Result struct {
					Value struct {
						State  string `json:"state"`
						Reward string `json:"reward"`
						Reason string `json:"reason"`
					} `json:"value"`
				} `json:"result"`
			}
			if err := json.Unmarshal(resRaw, &evalResult); err != nil {
				continue
			}

			val := evalResult.Result.Value
			switch val.State {
			case "ready":
				if !clicked {
					// Mark before sending: an ambiguous CDP reply must not repeat a mutation.
					clicked = true
					_, err := client.Call(runCtx, "Runtime.evaluate", map[string]interface{}{
						"expression": `(() => { const b=document.querySelector('#checkin-btn, .checkin-btn, button[data-action="checkin"]'); if(b && !b.disabled && b.getClientRects().length) b.click(); })()`,
					})
					if err != nil {
						return CheckinResult{}, fmt.Errorf("checkin click outcome unknown: %w", err)
					}
				}
			case "success":
				return CheckinResult{
					Success: true,
					Reward:  strings.TrimSpace(val.Reward),
					Message: "checkin succeeded via cdp automation",
				}, nil
			case "turnstile_blocked":
				// Fail closed on Turnstile challenge / manual gate
				return CheckinResult{
					Success: false,
					Message: val.Reason,
				}, ErrCDPNeedManual
			}
		}
	}
}

// ErrCDPNeedManual is returned when Turnstile challenge or human action is required.
var ErrCDPNeedManual = errors.New("turnstile or human verification required: manual checkin required")

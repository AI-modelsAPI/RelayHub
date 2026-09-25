package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// ProxyURL is the channel's egress proxy ("" = system, "direct" = none).
	// The browser must exit through the same path as the gateway and the
	// HTTP adapters (AUDIT RH-10), otherwise a site sees the account from two
	// networks.
	ProxyURL string `json:"proxy_url,omitempty"`
	// Selectors override the default DOM hooks (see SelectorsFromCapabilities).
	Selectors Selectors `json:"selectors,omitempty"`
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

// readDevToolsActivePort parses the port Chrome wrote to DevToolsActivePort
// (first line: port, second line: browser websocket path).
func readDevToolsActivePort(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	first, _, _ := strings.Cut(string(data), "\n")
	port, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid DevToolsActivePort contents %q", first)
	}
	return port, nil
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

	// Chrome picks its own debugging port (--remote-debugging-port=0) and
	// reports it in <profile>/DevToolsActivePort. Pre-selecting a port with
	// findFreePort left a window in which another local process could bind
	// it and impersonate the DevTools endpoint (AUDIT 2026-09-24 F15). The
	// profile directory is private to this user.
	if err := os.MkdirAll(userDataDir, 0o700); err != nil {
		return CheckinResult{}, fmt.Errorf("failed to create profile dir: %w", err)
	}
	_ = os.Chmod(userDataDir, 0o700)
	activePortFile := filepath.Join(userDataDir, "DevToolsActivePort")
	_ = os.Remove(activePortFile)
	port := 0

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Launch headless browser with remote debugging port, exiting through
	// the channel's egress proxy when one is configured (launchArgs refuses
	// credentialed proxies rather than silently going direct).
	args, err := launchArgs(port, userDataDir, req.ProxyURL)
	if err != nil {
		return CheckinResult{}, err
	}

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
		if port == 0 {
			p, perr := readDevToolsActivePort(activePortFile)
			if perr != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			port = p
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

	// JS expression to probe page state (see probeScript for the fail-closed
	// ordering). Selectors come from the request so sites can override them.
	probe := probeScript(req.Selectors)
	click := clickScript(req.Selectors)

	clicked := false
	for {
		select {
		case <-runCtx.Done():
			return CheckinResult{}, fmt.Errorf("checkin flow timed out: %w", runCtx.Err())
		case <-pollTicker.C:
			resRaw, err := client.Call(runCtx, "Runtime.evaluate", map[string]interface{}{
				"expression":    probe,
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
						"expression": click,
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

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// The profile directory is private to this user (AUDIT 2026-09-24 F15).
	if err := os.MkdirAll(userDataDir, 0o700); err != nil {
		return CheckinResult{}, fmt.Errorf("failed to create profile dir: %w", err)
	}
	_ = os.Chmod(userDataDir, 0o700)

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Launch headless browser with remote debugging port, exiting through
	// the channel's egress proxy when one is configured (launchArgs refuses
	// credentialed proxies rather than silently going direct).
	args, err := launchArgs(userDataDir, req.ProxyURL)
	if err != nil {
		return CheckinResult{}, err
	}

	// DevTools runs over a pair of pipes (--remote-debugging-pipe): the
	// browser reads commands from fd 3 and writes replies to fd 4. A TCP
	// debugging port — even one Chrome picks itself — is reachable by every
	// local process and user, and CDP has no authentication: anyone could
	// attach while the check-in runs and read the site's session cookies
	// (AUDIT 2026-09-24 F15).
	cmdR, cmdW, err := os.Pipe()
	if err != nil {
		return CheckinResult{}, fmt.Errorf("failed to create devtools pipe: %w", err)
	}
	respR, respW, err := os.Pipe()
	if err != nil {
		_ = cmdR.Close()
		_ = cmdW.Close()
		return CheckinResult{}, fmt.Errorf("failed to create devtools pipe: %w", err)
	}
	cmd := exec.CommandContext(runCtx, info.Path, args...)
	cmd.ExtraFiles = []*os.File{cmdR, respW} // child fd 3 and fd 4
	if err := e.runtime.StartProcess(cmd); err != nil {
		for _, f := range []*os.File{cmdR, cmdW, respR, respW} {
			_ = f.Close()
		}
		return CheckinResult{}, fmt.Errorf("failed to start browser process: %w", err)
	}
	// The child holds its own copies; closing ours lets the reader see EOF
	// as soon as the browser exits.
	_ = cmdR.Close()
	_ = respW.Close()
	// Guarantee process tree cleanup on exit
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := e.runtime.StopProcess(cleanupCtx, cmd); err != nil {
			out.Success = false
			outErr = errors.Join(outErr, fmt.Errorf("browser cleanup failed: %w", err))
		}
	}()

	client := cdp.ConnectPipe(respR, cmdW)
	defer client.Close()

	// Attaching waits for the browser to come up, which can take a large
	// part of the budget on a loaded machine (seen under the -race suite on
	// CI), so it shares the run's overall timeout instead of a tighter one.
	page, err := cdp.AttachFirstPage(runCtx, client)
	if err != nil {
		if runCtx.Err() != nil {
			return CheckinResult{}, runCtx.Err()
		}
		return CheckinResult{}, fmt.Errorf("failed to attach to browser page: %w", err)
	}

	// Enable Page & Runtime domains
	if _, err := page.Call(runCtx, "Page.enable", nil); err != nil {
		return CheckinResult{}, fmt.Errorf("cdp Page.enable failed: %w", err)
	}
	if _, err := page.Call(runCtx, "Runtime.enable", nil); err != nil {
		return CheckinResult{}, fmt.Errorf("cdp Runtime.enable failed: %w", err)
	}

	// Navigate to target URL
	_, err = page.Call(runCtx, "Page.navigate", map[string]interface{}{
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
			resRaw, err := page.Call(runCtx, "Runtime.evaluate", map[string]interface{}{
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
					_, err := page.Call(runCtx, "Runtime.evaluate", map[string]interface{}{
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

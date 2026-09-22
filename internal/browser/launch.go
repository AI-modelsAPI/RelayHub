package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Selectors are the DOM hooks the CDP flow uses on a check-in page. They are
// declarative so a site can be supported without a code change: put
// {"checkin_button":"...","checkin_success":"..."} in Provider.Capabilities.
type Selectors struct {
	// Button matches the visible, enabled element to click once.
	Button string `json:"checkin_button,omitempty"`
	// Success matches the element whose visibility is *only a hint* that the
	// server may now hold a record; the scheduler always verifies server-side.
	Success string `json:"checkin_success,omitempty"`
}

// DefaultSelectors match the conventions used by RelayHub's own fixtures and
// several new-api front-ends.
func DefaultSelectors() Selectors {
	return Selectors{
		Button:  `#checkin-btn, .checkin-btn, button[data-action="checkin"]`,
		Success: `.checkin-success, #checkin-success, [data-checkin-status="success"]`,
	}
}

// withDefaults fills empty fields from DefaultSelectors.
func (s Selectors) withDefaults() Selectors {
	d := DefaultSelectors()
	if strings.TrimSpace(s.Button) == "" {
		s.Button = d.Button
	}
	if strings.TrimSpace(s.Success) == "" {
		s.Success = d.Success
	}
	return s
}

// SelectorsFromCapabilities extracts selector overrides from a provider's
// free-form capabilities field. Anything that is not a JSON object with the
// known keys yields the defaults; the field is shared with other features.
func SelectorsFromCapabilities(raw string) Selectors {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") {
		return DefaultSelectors()
	}
	var s Selectors
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return DefaultSelectors()
	}
	return s.withDefaults()
}

// jsString renders s as a JavaScript string literal (JSON is a subset).
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// probeScript returns the page-state probe. Order is fail-closed: any
// unresolved human-verification gate yields to manual BEFORE any automated
// action is considered.
func probeScript(sel Selectors) string {
	sel = sel.withDefaults()
	return `(() => {
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

		// 3. Visible success evidence (a hint only; the caller verifies server-side)
		const successEl = document.querySelector(` + jsString(sel.Success) + `);
		if (successEl && successEl.getClientRects().length && getComputedStyle(successEl).visibility !== 'hidden') {
			return {
				state: 'success',
				reward: successEl.getAttribute('data-reward') || successEl.innerText || ''
			};
		}

		// 4. Visible, enabled action button -> report ready (no in-probe click)
		const btn = document.querySelector(` + jsString(sel.Button) + `);
		if (btn && !btn.disabled && btn.getClientRects().length) {
			return { state: 'ready' };
		}

		return { state: 'waiting' };
	})()`
}

// clickScript clicks the action button exactly once if it is actionable.
func clickScript(sel Selectors) string {
	sel = sel.withDefaults()
	return `(() => { const b=document.querySelector(` + jsString(sel.Button) + `); if(b && !b.disabled && b.getClientRects().length) b.click(); })()`
}

// ErrProxyCredentialsUnsupported is returned when a proxy URL carries
// user:password. Chromium does not accept credentials in --proxy-server and
// would prompt (or silently go direct after a 407), so we refuse to launch
// rather than exit through the wrong path.
var ErrProxyCredentialsUnsupported = errors.New("browser egress proxy with credentials is not supported by --proxy-server; use an unauthenticated local proxy")

// proxyArgs converts an egress proxy setting into Chromium flags.
//
//	""            -> no flag (system/environment proxy)
//	"direct"      -> --no-proxy-server
//	scheme://h:p  -> --proxy-server=scheme://h:p
func proxyArgs(proxyURL string) ([]string, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil, nil
	}
	if strings.EqualFold(proxyURL, "direct") || strings.EqualFold(proxyURL, "none") {
		return []string{"--no-proxy-server"}, nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid browser proxy url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "socks5", "socks4":
	case "socks5h":
		scheme = "socks5"
	default:
		return nil, fmt.Errorf("unsupported browser proxy scheme %q", u.Scheme)
	}
	if u.User != nil {
		return nil, ErrProxyCredentialsUnsupported
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid browser proxy url %q: missing host", proxyURL)
	}
	return []string{"--proxy-server=" + scheme + "://" + u.Host}, nil
}

// launchArgs assembles the Chromium command line for one check-in run.
func launchArgs(port int, userDataDir, proxyURL string) ([]string, error) {
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
	p, err := proxyArgs(proxyURL)
	if err != nil {
		return nil, err
	}
	args = append(args, p...)
	args = append(args, "about:blank")
	return args, nil
}

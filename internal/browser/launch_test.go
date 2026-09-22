package browser

import (
	"errors"
	"strings"
	"testing"
)

// The browser must exit through the channel's proxy like every other outbound
// path; a proxy with credentials cannot be expressed to Chromium and must
// refuse to launch rather than silently go direct.
func TestLaunchArgsProxy(t *testing.T) {
	has := func(args []string, want string) bool {
		for _, a := range args {
			if a == want {
				return true
			}
		}
		return false
	}
	args, err := launchArgs(9222, "/tmp/p", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--proxy-server") || a == "--no-proxy-server" {
			t.Fatalf("no proxy flag expected without proxy, got %v", args)
		}
	}
	if args[len(args)-1] != "about:blank" {
		t.Fatalf("about:blank must stay the final positional argument: %v", args)
	}
	args, err = launchArgs(9222, "/tmp/p", "socks5h://127.0.0.1:1080")
	if err != nil || !has(args, "--proxy-server=socks5://127.0.0.1:1080") {
		t.Fatalf("socks5 proxy flag missing: %v %v", args, err)
	}
	args, err = launchArgs(9222, "/tmp/p", "http://127.0.0.1:7890")
	if err != nil || !has(args, "--proxy-server=http://127.0.0.1:7890") {
		t.Fatalf("http proxy flag missing: %v %v", args, err)
	}
	args, err = launchArgs(9222, "/tmp/p", "direct")
	if err != nil || !has(args, "--no-proxy-server") {
		t.Fatalf("direct must disable proxies: %v %v", args, err)
	}
	if _, err := launchArgs(9222, "/tmp/p", "http://user:pw@127.0.0.1:7890"); !errors.Is(err, ErrProxyCredentialsUnsupported) {
		t.Fatalf("credentials must fail closed, got %v", err)
	}
	if _, err := launchArgs(9222, "/tmp/p", "ftp://x:1"); err == nil {
		t.Fatal("unsupported scheme must fail")
	}
}

func TestSelectorsFromCapabilities(t *testing.T) {
	d := DefaultSelectors()
	if got := SelectorsFromCapabilities(""); got != d {
		t.Fatalf("empty capabilities must yield defaults: %+v", got)
	}
	if got := SelectorsFromCapabilities("checkin,balance"); got != d {
		t.Fatalf("non-JSON capabilities must yield defaults: %+v", got)
	}
	got := SelectorsFromCapabilities(`{"checkin_button":"button.sign-in-daily","other":1}`)
	if got.Button != "button.sign-in-daily" || got.Success != d.Success {
		t.Fatalf("partial override must keep other defaults: %+v", got)
	}
}

// Selectors are embedded as JS string literals; quotes inside them must not
// break the script.
func TestProbeScriptEmbedsSelectorsSafely(t *testing.T) {
	sel := Selectors{Button: `button[data-x="sign"]`, Success: `div.ok'quoted`}
	probe := probeScript(sel)
	if !strings.Contains(probe, `document.querySelector("button[data-x=\"sign\"]")`) {
		t.Fatalf("button selector not embedded safely:\n%s", probe)
	}
	if !strings.Contains(probe, `document.querySelector("div.ok'quoted")`) {
		t.Fatalf("success selector not embedded safely:\n%s", probe)
	}
	if !strings.Contains(clickScript(sel), `"button[data-x=\"sign\"]"`) {
		t.Fatalf("click script missing selector: %s", clickScript(sel))
	}
	// Fail-closed ordering: turnstile check must precede success/ready checks.
	if strings.Index(probe, "turnstile_blocked") > strings.Index(probe, "state: 'success'") {
		t.Fatal("turnstile gate must be evaluated before success evidence")
	}
}

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"relayhub/internal/api"
	"relayhub/internal/app"
	"relayhub/internal/browser/cdp"
)

// TestConsolePairingInRealBrowser drives the shipped web console in a real
// Chromium (AUDIT 2026-09-24 F4): a #pair= link logs the console in and is
// stripped from the address bar, a spent link does not work twice, and the
// in-page gate (not window.prompt, which WKWebView lacks) accepts a fresh
// pairing link. Verified with Chromium 153 (@sparticuz/chromium).
//
// Opt-in: RELAYHUB_E2E_BROWSER=/path/to/chrome go test ./tests/e2e -run ConsolePairing
func TestConsolePairingInRealBrowser(t *testing.T) {
	chrome := os.Getenv("RELAYHUB_E2E_BROWSER")
	if chrome == "" {
		t.Skip("set RELAYHUB_E2E_BROWSER to a Chromium binary to run")
	}
	dir := t.TempDir()
	a, err := app.New(app.Config{DataDir: dir, HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true, ManagementAuth: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Shutdown(sctx)
	}()
	rt := a.Runtime()
	addr := rt.APIListener.Addr().String()
	raw, err := os.ReadFile(filepath.Join(dir, app.ManagementTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))

	// Chromium reads DevTools commands on fd 3 and answers on fd 4.
	cmdR, cmdW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	respR, respW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--headless=new", "--remote-debugging-pipe", "--user-data-dir=" + t.TempDir(), "--no-first-run", "--no-default-browser-check", "about:blank"}
	if os.Geteuid() == 0 {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.Command(chrome, args...)
	cmd.ExtraFiles = []*os.File{cmdR, respW}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("chromium stderr:\n%s", stderr.String())
		}
	})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = cmdR.Close()
	_ = respW.Close()
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	client := cdp.ConnectPipe(respR, cmdW)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sess, err := cdp.AttachFirstPage(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	eval := func(expr string) string {
		t.Helper()
		res, err := sess.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "awaitPromise": true, "returnByValue": true})
		if err != nil {
			t.Fatalf("evaluate %s: %v", expr, err)
		}
		var out struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
		}
		_ = json.Unmarshal(res, &out)
		return string(out.Result.Value)
	}
	waitTrue := func(what, expr string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if eval(expr) == "true" {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s (%s)", what, expr)
	}
	navigate := func(url string) {
		t.Helper()
		if _, err := sess.Call(ctx, "Page.navigate", map[string]any{"url": url}); err != nil {
			t.Fatal(err)
		}
	}
	mint := func() string {
		t.Helper()
		code, _, err := rt.APIServer.NewPairingCode()
		if err != nil {
			t.Fatal(err)
		}
		return api.PairingURL(addr, code)
	}
	const stored = `localStorage.getItem("relayhub.managementToken")`
	gateHidden := `(document.getElementById("auth-gate") || {}).hidden`

	// 1. A pairing link logs the console in and leaves no code behind.
	link := mint()
	navigate(link)
	waitTrue("the token from the pairing link", stored+" === "+strconv.Quote(token))
	if h := eval("location.hash"); h != `""` {
		t.Fatalf("the pairing code stayed in the address bar: %s", h)
	}
	waitTrue("the console to load without the gate", gateHidden+" === true")
	if st := eval(`fetch("/api/v1/settings", {headers: {Authorization: "Bearer " + ` + stored + `}}).then(r => r.status)`); st != "200" {
		t.Fatalf("stored token rejected: %s", st)
	}

	// 2. The spent link does not work twice: the gate appears instead.
	eval("localStorage.clear(), true")
	navigate("about:blank")
	navigate(link)
	waitTrue("the auth gate", gateHidden+" === false")
	if eval(stored) != "null" {
		t.Fatal("a spent pairing code yielded a token")
	}

	// 3. A wrong credential keeps the gate up with an error.
	eval(`(() => { document.getElementById("auth-input").value = "definitely-not-the-token"; document.getElementById("auth-gate").requestSubmit(); return true })()`)
	waitTrue("the gate error", `document.getElementById("auth-error").hidden === false`)

	// 4. The gate accepts a fresh pairing link and the console carries on.
	eval(`(() => { document.getElementById("auth-input").value = ` + strconv.Quote(mint()) + `; document.getElementById("auth-gate").requestSubmit(); return true })()`)
	waitTrue("the token from the gate", stored+" === "+strconv.Quote(token))
	waitTrue("the gate to close", gateHidden+" === true")
}

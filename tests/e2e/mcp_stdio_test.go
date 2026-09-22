package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relayhub/internal/app"
)

// TestMCPStdioBridgeAgainstRunningCore drives the real `relayhub mcp`
// subcommand as a subprocess against a live Core, the way Claude Code launches
// it from mcpServers.relayhub. It is the end-to-end guard for RH-25: the
// bridge used to serve a single request and the endpoint it forwarded to was
// never registered, so the advertised MCP capability was 100% dead while every
// unit test stayed green.
func TestMCPStdioBridgeAgainstRunningCore(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "relayhub")
	build := exec.Command("go", "build", "-o", bin, "./cmd/relayhub")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build relayhub: %v\n%s", err, out)
	}

	core, err := app.New(app.Config{
		DataDir:        filepath.Join(tmp, "data"),
		HTTPProxyAddr:  "127.0.0.1:0",
		SOCKS5Addr:     "127.0.0.1:0",
		GatewayAddr:    "127.0.0.1:0",
		ManagementAddr: "127.0.0.1:0",
		WireFullStack:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := core.Start(ctx); err != nil {
		t.Fatalf("start core: %v", err)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = core.Shutdown(shutCtx)
	}()
	mgmt := core.Runtime().APIListener.Addr().String()

	runCtx, runCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer runCancel()
	cmd := exec.CommandContext(runCtx, bin, "-management-addr", mgmt, "mcp")
	cmd.Env = append(os.Environ(), "RELAYHUB_MANAGEMENT_TOKEN=")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mcp bridge: %v", err)
	}
	reader := bufio.NewReader(stdout)

	// A persistent session: several requests on one stdin, each answered on
	// exactly one stdout line, in order.
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_channels","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"quota_status","arguments":{}}}`,
	}
	type rpcResp struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	responses := make([]rpcResp, 0, len(requests))
	for _, req := range requests {
		if _, err := stdin.Write([]byte(req + "\n")); err != nil {
			t.Fatalf("write request: %v", err)
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("no response line for %s: %v (stderr=%s)", req, err, stderr.String())
		}
		var resp rpcResp
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("response is not JSON-RPC: %q: %v", line, err)
		}
		responses = append(responses, resp)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("mcp bridge exited with error: %v (stderr=%s)", err, stderr.String())
	}

	// initialize
	if responses[0].Error != nil || !strings.Contains(string(responses[0].Result), `"protocolVersion"`) {
		t.Fatalf("initialize: %+v", responses[0])
	}
	// tools/list
	var tl struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(responses[2].Result, &tl); err != nil || len(tl.Tools) == 0 {
		t.Fatalf("tools/list: %s err=%v", responses[2].Result, err)
	}
	names := map[string]bool{}
	for _, tool := range tl.Tools {
		names[tool.Name] = true
		if len(tool.InputSchema) == 0 {
			t.Fatalf("tool %s has no inputSchema (MCP clients reject it)", tool.Name)
		}
	}
	for _, want := range []string{"list_channels", "quota_status", "explain_last_request", "run_checkin", "trust_report"} {
		if !names[want] {
			t.Fatalf("tools/list missing %s: %v", want, names)
		}
	}
	// tools/call returns content blocks and isError:false.
	for i := 3; i <= 4; i++ {
		var call struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(responses[i].Result, &call); err != nil || len(call.Content) == 0 || call.IsError {
			t.Fatalf("tools/call #%d bad result: %s err=%v", i, responses[i].Result, err)
		}
		if call.Content[0].Type != "text" {
			t.Fatalf("tools/call #%d: expected text content block, got %s", i, call.Content[0].Type)
		}
	}
}

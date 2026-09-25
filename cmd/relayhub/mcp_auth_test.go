package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"relayhub/internal/api"
)

// AUDIT 2026-09-24 F4: the MCP bridge authenticates with the management
// token, re-reads it per request, and turns a 401 into a JSON-RPC error.
func TestMCPBridgeWithManagementAuth(t *testing.T) {
	const token = "mcp-bridge-token-0123456789"
	s, err := api.NewConfiguredServer(api.Config{LocalOnly: true, Management: "127.0.0.1:8790", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	current := "" // the bridge starts before a token exists
	in := strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"tools/list"}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":8,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	calls := 0
	tokenFn := func() string {
		calls++
		if calls > 2 {
			current = token // RelayHub generated its token meanwhile
		}
		return current
	}
	if err := runMCPStdio(addr, tokenFn, in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 responses (the notification gets none), got %d: %q", len(lines), out.String())
	}
	var denied struct {
		ID    int `json:"id"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &denied); err != nil || denied.ID != 7 || denied.Error.Code != -32001 || !strings.Contains(denied.Error.Message, "RELAYHUB_DATA_DIR") {
		t.Fatalf("401 must become a JSON-RPC error for id 7: %s (%v)", lines[0], err)
	}
	if !strings.Contains(lines[1], `"id":8`) || !strings.Contains(lines[1], "tools") || strings.Contains(lines[1], `"error"`) {
		t.Fatalf("the request after the token appeared must succeed: %s", lines[1])
	}
}

package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

type stub struct{}

func (stub) ListChannels(context.Context) (any, error) { return []string{"c"}, nil }
func (stub) QuotaStatus(context.Context) (any, error)  { return map[string]int{"total": 1}, nil }
func (stub) ExplainLast(context.Context) (any, error)  { return "ok", nil }
func (stub) RunCheckin(context.Context) (any, error)   { return []string{"c1"}, nil }
func (stub) TrustReport(context.Context) (any, error)  { return []any{}, nil }

// sampleCheckinResult mirrors the map shape the production adapter returns so
// the content-block encoding test below serialises deterministically.
func sampleCheckinResult() any {
	return map[string]any{"ok": true, "started": []string{"c1"}, "skipped": []string{}}
}

func TestHandleListChannels(t *testing.T) {
	s := &Server{Backend: stub{}}
	out := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"list_channels"}`))
	var resp map[string]any
	if json.Unmarshal(out, &resp) != nil {
		t.Fatal(string(out))
	}
	if resp["result"] == nil {
		t.Fatalf("no result: %s", out)
	}
}

// Interop shape checks (AUDIT RH-25): lifecycle answers, tools/list carries
// inputSchema, tools/call wraps results in MCP content blocks.
func TestHandleMCPInterop(t *testing.T) {
	s := &Server{Backend: stub{}}

	init := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	var initResp map[string]any
	if err := json.Unmarshal(init, &initResp); err != nil {
		t.Fatal(err)
	}
	result, ok := initResp["result"].(map[string]any)
	if !ok || result["protocolVersion"] == "" || result["serverInfo"] == nil {
		t.Fatalf("initialize must return protocolVersion + serverInfo: %s", init)
	}

	list := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	var listResp map[string]any
	if err := json.Unmarshal(list, &listResp); err != nil {
		t.Fatal(err)
	}
	toolsResult, _ := listResp["result"].(map[string]any)
	tools, _ := toolsResult["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("tools/list returned no tools: %s", list)
	}
	tool0, _ := tools[0].(map[string]any)
	if tool0["inputSchema"] == nil {
		t.Fatalf("tool descriptor must include inputSchema: %s", list)
	}

	call := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_checkin"}}`))
	var callResp map[string]any
	if err := json.Unmarshal(call, &callResp); err != nil {
		t.Fatal(err)
	}
	callResult, _ := callResp["result"].(map[string]any)
	content, _ := callResult["content"].([]any)
	if len(content) == 0 || callResult["isError"] != false {
		t.Fatalf("tools/call must return MCP content blocks: %s", call)
	}

	unknown := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":4,"method":"nope/never"}`))
	var errResp map[string]any
	if err := json.Unmarshal(unknown, &errResp); err != nil {
		t.Fatal(err)
	}
	errObj, _ := errResp["error"].(map[string]any)
	if errObj["code"] != float64(-32601) {
		t.Fatalf("unknown method must surface JSON-RPC -32601: %s", unknown)
	}
}

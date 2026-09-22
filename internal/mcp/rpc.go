package mcp

import (
	"context"
	"encoding/json"
)

// Tool is one MCP tool descriptor including its input schema, as required by
// the MCP tools/list response shape (AUDIT RH-25).
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type Backend interface {
	ListChannels(ctx context.Context) (any, error)
	QuotaStatus(ctx context.Context) (any, error)
	ExplainLast(ctx context.Context) (any, error)
	RunCheckin(ctx context.Context) (any, error)
	TrustReport(ctx context.Context) (any, error)
}

type Server struct {
	Backend Backend
}

// protocolVersion is the MCP revision this server negotiates.
const protocolVersion = "2024-11-05"

var emptyObjectSchema = map[string]any{"type": "object", "properties": map[string]any{}}

func Tools() []Tool {
	return []Tool{
		{Name: "list_channels", Description: "List configured upstream channels", InputSchema: emptyObjectSchema},
		{Name: "quota_status", Description: "Summarise recent usage and cache hits", InputSchema: emptyObjectSchema},
		{Name: "explain_last_request", Description: "Explain the most recent gateway request", InputSchema: emptyObjectSchema},
		{Name: "run_checkin", Description: "Trigger check-in for all enabled channels", InputSchema: emptyObjectSchema},
		{Name: "trust_report", Description: "Return passive trust scores", InputSchema: emptyObjectSchema},
	}
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (s *Server) Handle(ctx context.Context, raw []byte) []byte {
	var req rpcReq
	if json.Unmarshal(raw, &req) != nil || req.Method == "" {
		return rpcErr(req.ID, -32600, "invalid request")
	}
	if s == nil || s.Backend == nil {
		return rpcErr(req.ID, -32603, "backend unavailable")
	}

	switch req.Method {
	case "initialize":
		return rpcOK(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "relayhub", "version": "0.1.0"},
		})
	case "ping":
		return rpcOK(req.ID, map[string]any{})
	}
	// Notifications (lifecycle and otherwise) are accepted and acknowledged;
	// over the HTTP transport there is no way to stay silent.
	if len(req.Method) > 13 && req.Method[:13] == "notifications" {
		return rpcOK(req.ID, map[string]any{})
	}

	name := req.Method
	var params struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(req.Params, &params)
	isToolCall := false
	if name == "tools/call" && params.Name != "" {
		name = params.Name
		isToolCall = true
	}
	if name == "tools/list" {
		return rpcOK(req.ID, map[string]any{"tools": Tools()})
	}

	var (
		out any
		err error
	)
	switch name {
	case "list_channels":
		out, err = s.Backend.ListChannels(ctx)
	case "quota_status":
		out, err = s.Backend.QuotaStatus(ctx)
	case "explain_last_request":
		out, err = s.Backend.ExplainLast(ctx)
	case "run_checkin":
		out, err = s.Backend.RunCheckin(ctx)
	case "trust_report":
		out, err = s.Backend.TrustReport(ctx)
	default:
		return rpcErr(req.ID, -32601, "method not found: "+req.Method)
	}
	if isToolCall {
		// MCP tools/call results are content blocks, and backend failures are
		// reported with isError rather than a protocol-level error (RH-25).
		if err != nil {
			return rpcOK(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			})
		}
		b, marshalErr := json.Marshal(out)
		if marshalErr != nil {
			return rpcErr(req.ID, -32603, marshalErr.Error())
		}
		return rpcOK(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(b)}},
			"isError": false,
		})
	}
	if err != nil {
		return rpcErr(req.ID, -32603, err.Error())
	}
	return rpcOK(req.ID, out)
}

func rpcOK(id any, result any) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	return b
}

func rpcErr(id any, code int, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	return b
}

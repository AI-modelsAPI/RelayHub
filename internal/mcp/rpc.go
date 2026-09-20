package mcp

import (
	"context"
	"encoding/json"
)

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
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

func Tools() []Tool {
	return []Tool{
		{Name: "list_channels", Description: "List configured upstream channels"},
		{Name: "quota_status", Description: "Summarise recent usage and cache hits"},
		{Name: "explain_last_request", Description: "Explain the most recent gateway request"},
		{Name: "run_checkin", Description: "Trigger check-in for all enabled channels"},
		{Name: "trust_report", Description: "Return passive trust scores"},
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
	if json.Unmarshal(raw, &req) != nil {
		return rpcErr(nil, "invalid request")
	}
	name := req.Method
	var params struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(req.Params, &params)
	if name == "tools/call" && params.Name != "" {
		name = params.Name
	}
	if s == nil || s.Backend == nil {
		return rpcErr(req.ID, "backend unavailable")
	}
	var (
		out any
		err error
	)
	switch name {
	case "list_channels", "tools/list":
		if name == "tools/list" {
			out = map[string]any{"tools": Tools()}
			break
		}
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
		return rpcErr(req.ID, "unknown method")
	}
	if err != nil {
		return rpcErr(req.ID, err.Error())
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": out})
	return b
}

func rpcErr(id any, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"message": msg}})
	return b
}

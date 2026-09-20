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
func (stub) RunCheckin(context.Context) (any, error)   { return true, nil }
func (stub) TrustReport(context.Context) (any, error)  { return []any{}, nil }

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

package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"relayhub/internal/affinity"
	"relayhub/internal/lab"
	"relayhub/internal/ratelimit"
	"relayhub/internal/verify"
)

// TestManagementRouteTableIsComplete is the regression guard for the class of
// defect behind P0-1 / RH-05: a whole family of handlers (extras.go) existed,
// was fully wired with dependencies, and was never registered on the mux, so
// the UI, MCP bridge and routes/explain silently 404'd in production. Every
// endpoint the frontend, the MCP stdio bridge or the docs reference must be
// listed here and must answer with something other than the JSON 404 the
// fallback handler emits for unknown /api/ paths.
func TestManagementRouteTableIsComplete(t *testing.T) {
	s, err := NewConfiguredServer(Config{LocalOnly: true, Management: "127.0.0.1:8790"})
	if err != nil {
		t.Fatal(err)
	}
	s.WithControlPlane(verify.New(), lab.NewRing(4), affinity.New(0), ratelimit.New())
	h := s.Handler()

	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/healthz", ""},
		{http.MethodGet, "/api/v1/health", ""},
		{http.MethodGet, "/api/v1/overview", ""},
		{http.MethodGet, "/api/v1/providers", ""},
		{http.MethodGet, "/api/v1/channels", ""},
		{http.MethodGet, "/api/v1/models", ""},
		{http.MethodGet, "/api/v1/provider-models", ""},
		{http.MethodGet, "/api/v1/model-groups", ""},
		{http.MethodGet, "/api/v1/routes", ""},
		{http.MethodGet, "/api/v1/checkin", ""},
		{http.MethodGet, "/api/v1/logs", ""},
		{http.MethodGet, "/api/v1/usage", ""},
		{http.MethodGet, "/api/v1/settings", ""},
		{http.MethodGet, "/api/v1/browser", ""},
		{http.MethodGet, "/api/v1/cli-sync", ""},
		{http.MethodGet, "/api/v1/import-export", ""},
		{http.MethodGet, "/api/v1/secrets", ""},
		{http.MethodGet, "/api/v1/keys", ""},
		{http.MethodGet, "/api/v1/channel-keys", ""},
		{http.MethodGet, "/api/v1/channels/stats", ""},
		{http.MethodGet, "/api/v1/models/catalog", ""},
		// extras.go family (P0-1 / RH-05)
		{http.MethodGet, "/api/v1/usage/summary", ""},
		{http.MethodGet, "/api/v1/verify/scores", ""},
		{http.MethodPost, "/api/v1/verify/probe", `{"channel_id":"none"}`},
		{http.MethodGet, "/api/v1/identity", ""},
		{http.MethodPatch, "/api/v1/identity/none", `{}`},
		{http.MethodGet, "/api/v1/sessions", ""},
		{http.MethodGet, "/api/v1/lab", ""},
		{http.MethodPost, "/api/v1/lab/capture", `{"enabled":false}`},
		{http.MethodDelete, "/api/v1/lab/capture", ""},
		{http.MethodPost, "/api/v1/lab/replay", `{}`},
		{http.MethodGet, "/api/v1/routes/explain", ""},
		{http.MethodPost, "/api/v1/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
	}

	for _, rt := range routes {
		w := request(t, h, rt.method, rt.path, "", rt.body)
		if w.Code == http.StatusNotFound {
			// Distinguish "route missing" (fallback envelope, code not_found)
			// from a handler that legitimately answers 404 for a resource.
			var env struct {
				Error Error `json:"error"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			if env.Error.Code == "not_found" && env.Error.Message == unknownEndpointMessage {
				t.Errorf("%s %s is not registered on the management mux (fell through to the JSON 404 handler)", rt.method, rt.path)
			}
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s %s answered with Content-Type %q; every /api route must speak JSON (got status %d)", rt.method, rt.path, ct, w.Code)
		}
	}

	// Unknown API paths must be a JSON 404, never the SPA document.
	w := request(t, h, http.MethodGet, "/api/v1/does-not-exist", "", "")
	if w.Code != http.StatusNotFound || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unknown /api path: status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
	}
}

// The extras handlers are registered per path; each must keep its method
// contract so a wrong verb is a 405 envelope, not a silent 200.
func TestExtrasEndpointsEnforceMethods(t *testing.T) {
	s, err := NewConfiguredServer(Config{LocalOnly: true, Management: "127.0.0.1:8790"})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/usage/summary"},
		{http.MethodPost, "/api/v1/verify/scores"},
		{http.MethodGet, "/api/v1/verify/probe"},
		{http.MethodPost, "/api/v1/identity"},
		{http.MethodGet, "/api/v1/lab/capture"},
		{http.MethodGet, "/api/v1/lab/replay"},
		{http.MethodPost, "/api/v1/routes/explain"},
		{http.MethodGet, "/api/v1/mcp"},
	} {
		w := request(t, h, tc.method, tc.path, "", "")
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: expected 405, got %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

// MCP over the management API must answer a real tools/list so the
// `relayhub mcp` stdio bridge (and Claude Code's mcpServers.relayhub entry)
// has something to talk to (RH-25).
func TestMCPEndpointServesToolsList(t *testing.T) {
	s, err := NewConfiguredServer(Config{LocalOnly: true, Management: "127.0.0.1:8790"})
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, s.Handler(), http.MethodPost, "/api/v1/mcp", "", `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ID     any `json:"id"`
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("not json-rpc: %v (%s)", err, w.Body.String())
	}
	if resp.Error != nil || len(resp.Result.Tools) == 0 {
		t.Fatalf("tools/list failed: %s", w.Body.String())
	}
}

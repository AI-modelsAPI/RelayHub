package api

import (
	"net/http"
	"testing"

	"relayhub/internal/affinity"
	"relayhub/internal/lab"
	"relayhub/internal/ratelimit"
	"relayhub/internal/verify"
)

// AUDIT 2026-09-24 F11: the extras handlers never called s.authorize, so a
// configured management token did not protect them. Every mutating /api/
// call must now be refused without the token and accepted with it.
func TestManagementTokenProtectsEveryMutatingEndpoint(t *testing.T) {
	s, err := NewConfiguredServer(Config{LocalOnly: true, Management: "127.0.0.1:8790", Token: "t0ken"})
	if err != nil {
		t.Fatal(err)
	}
	s.WithControlPlane(verify.New(), lab.NewRing(4), affinity.New(0), ratelimit.New())
	h := s.Handler()
	mutations := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/verify/probe", `{"channel_id":"none"}`},
		{http.MethodPatch, "/api/v1/identity/none", `{}`},
		{http.MethodPost, "/api/v1/lab/capture", `{"enabled":true}`},
		{http.MethodDelete, "/api/v1/lab/capture", ``},
		{http.MethodPost, "/api/v1/lab/replay", `{}`},
		{http.MethodPost, "/api/v1/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
		{http.MethodPost, "/api/v1/sessions", `{}`},
		{http.MethodPost, "/api/v1/routes/explain", `{"model":"m"}`},
		{http.MethodPost, "/api/v1/usage/summary", `{}`},
		{http.MethodPost, "/api/v1/keys", `{}`},
		{http.MethodPost, "/api/v1/checkin", `{}`},
		{http.MethodPost, "/api/v1/cli-sync", `{"cli":"claude","action":"preview"}`},
		{http.MethodPost, "/api/v1/channels", `{}`},
	}
	for _, m := range mutations {
		if w := request(t, h, m.method, m.path, "", m.body); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token: status %d, want 401 (%s)", m.method, m.path, w.Code, w.Body.String())
		}
		if w := request(t, h, m.method, m.path, "t0ken", m.body); w.Code == http.StatusUnauthorized {
			t.Fatalf("%s %s with token: still 401", m.method, m.path)
		}
	}
	// Liveness stays public.
	if w := request(t, h, http.MethodGet, "/api/v1/health", "", ""); w.Code != http.StatusOK {
		t.Fatalf("health: %d", w.Code)
	}
}

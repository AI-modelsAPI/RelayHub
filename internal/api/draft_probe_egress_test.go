package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// AUDIT 2026-09-24 F7: draft fetch-models probes used a bare direct client,
// bypassing the configured global egress.
func TestDraftModelProbeUsesGlobalEgress(t *testing.T) {
	var proxied atomic.Int32
	egressProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "relay.invalid" {
			http.Error(w, "unexpected target", http.StatusBadGateway)
			return
		}
		proxied.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"}]}`))
	}))
	defer egressProxy.Close()
	s, err := NewConfiguredServer(Config{LocalOnly: true, Management: "127.0.0.1:8790", DefaultEgress: egressProxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, s.Handler(), http.MethodPost, "/api/v1/fetch-models", "", `{"base_url":"http://relay.invalid","api_key":"sk-draft"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gpt-4o") {
		t.Fatalf("draft probe: %d %s", w.Code, w.Body.String())
	}
	if proxied.Load() == 0 {
		t.Fatal("draft probe bypassed the global egress proxy")
	}
}

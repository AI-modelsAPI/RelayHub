package gateway_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// AUDIT 2026-09-24 F9: an upstream 302 made the gateway GET an internal URL
// and return that body to the client as a 200 completion.
func TestGatewayDoesNotFollowUpstreamRedirects(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secret":"internal-only"}`))
	}))
	defer internal.Close()
	for _, code := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, internal.URL+"/api/v1/settings", code)
		}))
		gw := headerTestGateway(up.URL)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		gw.ServeHTTP(w, req)
		up.Close()
		if w.Code < 400 || strings.Contains(w.Body.String(), "internal-only") || w.Header().Get("Location") != "" {
			t.Fatalf("redirect %d: status %d location %q body %s", code, w.Code, w.Header().Get("Location"), w.Body.String())
		}
	}
	if n := internalHits.Load(); n != 0 {
		t.Fatalf("gateway followed the redirect to the internal server %d time(s)", n)
	}
}

func TestGatewayRejectsHTMLSuccessResponses(t *testing.T) {
	for _, stream := range []bool{false, true} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><title>Just a moment...</title></html>"))
		}))
		gw := headerTestGateway(up.URL)
		body := `{"model":"m1","messages":[{"role":"user","content":"hi"}]}`
		if stream {
			body = `{"model":"m1","stream":true,"messages":[{"role":"user","content":"hi"}]}`
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		gw.ServeHTTP(w, req)
		up.Close()
		if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "Just a moment") {
			t.Fatalf("stream=%v: status %d body %s", stream, w.Code, w.Body.String())
		}
	}
}

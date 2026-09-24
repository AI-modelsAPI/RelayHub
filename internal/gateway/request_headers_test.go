package gateway_test

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/gateway"
	"relayhub/internal/health"
	"relayhub/internal/router"
)

func headerTestGateway(base string) *gateway.Handler {
	ch := domain.Channel{ID: "c1", ProviderID: "p1", BaseURL: base, Enabled: true, RoutingEnabled: true}
	m := domain.Model{ID: "m1", Enabled: true}
	pm := domain.ProviderModel{ID: "pm1", ProviderID: "p1", ChannelID: "c1", ModelID: "m1", UpstreamModelName: "gpt-4", Protocol: "openai", Enabled: true}
	res := &router.Resolver{Models: map[string]domain.Model{"m1": m}, Channels: map[string]domain.Channel{"c1": ch}, ProviderModels: []domain.ProviderModel{pm}, Health: health.NewRegistry()}
	return gateway.New(gateway.Config{Resolver: res, Upstream: gateway.HTTPUpstream{}, Health: res.Health})
}

const chatCompletion = `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

// AUDIT 2026-09-24 F8: a client Accept-Encoding header was forwarded, the
// upstream gzipped its reply, and the gateway failed to parse it (502).
func TestGatewayDoesNotForwardClientAcceptEncoding(t *testing.T) {
	var mu sync.Mutex
	seen := http.Header{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			_, _ = gz.Write([]byte(chatCompletion))
			_ = gz.Close()
			return
		}
		_, _ = w.Write([]byte(chatCompletion))
	}))
	defer up.Close()
	gw := headerTestGateway(up.URL)

	for _, ae := range []string{"", "gzip, deflate", "gzip, deflate, br, zstd"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost:3000")
		req.Header.Set("OpenAI-Organization", "org-private")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("X-Stainless-Os", "MacOS")
		if ae != "" {
			req.Header.Set("Accept-Encoding", ae)
		}
		w := httptest.NewRecorder()
		gw.ServeHTTP(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"content":"ok"`) {
			t.Fatalf("Accept-Encoding=%q: status %d body %s", ae, w.Code, w.Body.String())
		}
		mu.Lock()
		for _, h := range []string{"Origin", "Openai-Organization", "Sec-Fetch-Site"} {
			if v := seen.Get(h); v != "" {
				t.Fatalf("upstream received %s=%q", h, v)
			}
		}
		if ae != "" && seen.Get("Accept-Encoding") == ae {
			t.Fatalf("client Accept-Encoding %q forwarded verbatim", ae)
		}
		mu.Unlock()
	}
}

func TestGatewayRejectsOversizedBodiesInsteadOfTruncating(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","pad":"`))
		_, _ = w.Write(bytes.Repeat([]byte("a"), 17<<20))
		_, _ = w.Write([]byte(`"}`))
	}))
	defer up.Close()
	gw := headerTestGateway(up.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "response_too_large") {
		t.Fatalf("oversized upstream response: status %d body %.200s", w.Code, w.Body.String())
	}

	big := append([]byte(`{"model":"m1","messages":[{"role":"user","content":"`), bytes.Repeat([]byte("a"), 17<<20)...)
	big = append(big, []byte(`"}]}`)...)
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	gw.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request: status %d body %.200s", w.Code, w.Body.String())
	}
}

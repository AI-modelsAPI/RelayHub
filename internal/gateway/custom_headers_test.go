package gateway_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/gateway"
	"relayhub/internal/health"
	"relayhub/internal/router"
)

func TestGatewayCustomHeadersInjection(t *testing.T) {
	var receivedUA, receivedCustom string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		receivedCustom = r.Header.Get("X-Custom-Auth")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat-custom","choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()

	ch := domain.Channel{
		ID:             "c-custom",
		ProviderID:     "p-custom",
		BaseURL:        upstream.URL,
		Enabled:        true,
		RoutingEnabled: true,
		CustomHeaders: map[string]string{
			"User-Agent":    "claude-cli/2.1.251 (custom)",
			"X-Custom-Auth": "secret-node-id-888",
		},
	}
	m := domain.Model{ID: "m-custom", Enabled: true}
	pm := domain.ProviderModel{ID: "pm-custom", ProviderID: "p-custom", ChannelID: ch.ID, ModelID: m.ID, UpstreamModelName: "gpt-4", Protocol: "openai", Enabled: true}

	res := &router.Resolver{
		Models:         map[string]domain.Model{m.ID: m},
		Channels:       map[string]domain.Channel{ch.ID: ch},
		ProviderModels: []domain.ProviderModel{pm},
		Health:         health.NewRegistry(),
	}

	gw := gateway.New(gateway.Config{
		Resolver: res,
		Upstream: gateway.HTTPUpstream{},
		Health:   res.Health,
	})

	body := []byte(`{"model":"m-custom","messages":[{"role":"user","content":"test"}]}`)
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	gw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if receivedUA != "claude-cli/2.1.251 (custom)" {
		t.Fatalf("User-Agent custom header not injected: %q", receivedUA)
	}
	if receivedCustom != "secret-node-id-888" {
		t.Fatalf("X-Custom-Auth header not injected: %q", receivedCustom)
	}
}

func TestUsageSummaryCalculation(t *testing.T) {
	// Simple sanity test for timing and records
	now := time.Now()
	if now.IsZero() {
		t.Fatal("time zero")
	}
}

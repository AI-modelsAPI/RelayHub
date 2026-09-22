package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"relayhub/internal/notify"
)

// proxy_url is now consumed by every outbound path, so a typo must be
// rejected at the API instead of failing closed at 08:00 tomorrow.
func TestChannelProxyURLValidatedAndExplained(t *testing.T) {
	srv, _ := newCatalogServer(t)
	srv.DefaultEgress = "socks5://user:secret@127.0.0.1:1080"
	h := srv.Handler()

	w := catalogDo(t, h, http.MethodPut, "/api/v1/channels/c", `{"provider_id":"p","name":"C","base_url":"https://up.test","enabled":true,"proxy_url":"ftp://127.0.0.1:21"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "proxy_url") {
		t.Fatalf("invalid proxy_url must be rejected: %d %s", w.Code, w.Body.String())
	}
	w = catalogDo(t, h, http.MethodPut, "/api/v1/channels/c", `{"provider_id":"p","name":"C","base_url":"https://up.test","enabled":true,"proxy_url":"http://pw:hidden@127.0.0.1:7890"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("valid proxy_url rejected: %d %s", w.Code, w.Body.String())
	}
	w = catalogDo(t, h, http.MethodGet, "/api/v1/channels/stats?channel_id=c", "")
	if w.Code != http.StatusOK {
		t.Fatalf("stats status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Stats struct {
			Egress map[string]string `json:"egress"`
		} `json:"stats"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Stats.Egress["source"] != "channel" || out.Stats.Egress["via"] != "http://pw:***@127.0.0.1:7890" {
		t.Fatalf("egress view must name the channel exit without credentials: %+v", out.Stats.Egress)
	}
	if strings.Contains(w.Body.String(), "hidden") || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("credentials leaked into stats: %s", w.Body.String())
	}

	// Clearing the channel proxy falls back to the global default (masked).
	w = catalogDo(t, h, http.MethodPut, "/api/v1/channels/c", `{"provider_id":"p","name":"C","base_url":"https://up.test","enabled":true,"proxy_url":"direct"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("direct must be accepted: %d %s", w.Code, w.Body.String())
	}
	w = catalogDo(t, h, http.MethodGet, "/api/v1/settings", "")
	if !strings.Contains(w.Body.String(), `"default":"socks5://user:***@127.0.0.1:1080"`) || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("settings must describe the default egress without credentials: %s", w.Body.String())
	}
}

func TestNotifyTestEndpoint(t *testing.T) {
	var hits int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), `"kind":"test"`) {
			atomic.AddInt32(&hits, 1)
		}
	}))
	defer sink.Close()

	srv, _ := newCatalogServer(t)
	h := srv.Handler()
	w := catalogDo(t, h, http.MethodPost, "/api/v1/notify/test", "")
	if w.Code == http.StatusAccepted {
		t.Fatalf("without sinks the endpoint must say unsupported, got %d", w.Code)
	}
	w = catalogDo(t, h, http.MethodGet, "/api/v1/settings", "")
	if !strings.Contains(w.Body.String(), `"notify":{"enabled":false`) {
		t.Fatalf("settings must report notify disabled: %s", w.Body.String())
	}

	srv.Notifier = notify.NewDispatcher(notify.Options{}, notify.NewWebhook(sink.URL, sink.Client()))
	h = srv.Handler()
	w = catalogDo(t, h, http.MethodPost, "/api/v1/notify/test", "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("notify test status=%d body=%s", w.Code, w.Body.String())
	}
	srv.Notifier.Flush(5 * time.Second)
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("test event not delivered to webhook (hits=%d)", hits)
	}
}

// Admin probes (model fetch / connectivity test) must resolve the upstream
// client through the egress package like every other outbound path. They used
// to go through identity.HTTPClientE, which did not understand the "direct"
// sentinel the API accepts on write, so a channel saved with proxy_url=direct
// failed its own connectivity test with a 400 (found while merging the egress
// line onto the audited main).
func TestAdminProbesHonourDirectEgressSentinel(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m-direct"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	srv, _ := newCatalogServer(t)
	// A global default that would break the probe if it were applied to a
	// channel that explicitly asked for a direct exit.
	srv.DefaultEgress = "socks5://127.0.0.1:1"
	h := srv.Handler()

	w := catalogDo(t, h, http.MethodPut, "/api/v1/channels/c", `{"provider_id":"p","name":"C","base_url":"`+upstream.URL+`","enabled":true,"proxy_url":"direct"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("direct must be accepted on write: %d %s", w.Code, w.Body.String())
	}

	w = catalogDo(t, h, http.MethodPost, "/api/v1/fetch-models", `{"channel_id":"c"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "m-direct") {
		t.Fatalf("fetch-models with proxy_url=direct: %d %s", w.Code, w.Body.String())
	}
	w = catalogDo(t, h, http.MethodPost, "/api/v1/channels/test", `{"channel_id":"c"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"success":true`) || !strings.Contains(w.Body.String(), `"model_count":1`) {
		t.Fatalf("channels/test with proxy_url=direct: %d %s", w.Code, w.Body.String())
	}
	if atomic.LoadInt32(&hits) < 2 {
		t.Fatalf("upstream must have been reached directly twice, got %d", hits)
	}

	// Without a channel override the global default applies; a dead proxy
	// must surface as an upstream error, never as a silent direct fallback.
	w = catalogDo(t, h, http.MethodPut, "/api/v1/channels/c", `{"provider_id":"p","name":"C","base_url":"`+upstream.URL+`","enabled":true,"proxy_url":""}`)
	if w.Code != http.StatusOK {
		t.Fatalf("clearing proxy_url: %d %s", w.Code, w.Body.String())
	}
	before := atomic.LoadInt32(&hits)
	w = catalogDo(t, h, http.MethodPost, "/api/v1/fetch-models", `{"channel_id":"c"}`)
	if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "m-direct") {
		t.Fatalf("global egress socks5://127.0.0.1:1 must not be bypassed: %d %s", w.Code, w.Body.String())
	}
	if atomic.LoadInt32(&hits) != before {
		t.Fatalf("upstream was reached directly despite a global egress proxy")
	}
}

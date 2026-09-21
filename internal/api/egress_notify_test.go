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

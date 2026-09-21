package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type capture struct {
	mu   sync.Mutex
	reqs []capturedReq
}
type capturedReq struct {
	path, body, contentType string
	form                    map[string]string
}

func (c *capture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cr := capturedReq{path: r.URL.Path, body: string(body), contentType: r.Header.Get("Content-Type"), form: map[string]string{}}
		if strings.HasPrefix(cr.contentType, "application/x-www-form-urlencoded") {
			_ = r.Body.Close()
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			_ = r.ParseForm()
			for k := range r.PostForm {
				cr.form[k] = r.PostForm.Get(k)
			}
		}
		c.mu.Lock()
		c.reqs = append(c.reqs, cr)
		c.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}
func (c *capture) all() []capturedReq {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedReq(nil), c.reqs...)
}

func TestWebhookPostsJSONEvent(t *testing.T) {
	c := &capture{}
	srv := httptest.NewServer(c.handler())
	defer srv.Close()
	n := NewWebhook(srv.URL, srv.Client())
	ev := Event{Kind: KindCheckinFailed, Severity: SeverityError, Title: "签到失败", Body: "SeekAI: HTTP 502", ChannelID: "c1", Fields: map[string]string{"channel": "SeekAI"}, At: time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)}
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	got := c.all()
	if len(got) != 1 || !strings.HasPrefix(got[0].contentType, "application/json") {
		t.Fatalf("expected one JSON post, got %+v", got)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got[0].body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["kind"] != string(KindCheckinFailed) || payload["title"] != "签到失败" || payload["channel_id"] != "c1" || payload["source"] != "relayhub" {
		t.Fatalf("payload: %v", payload)
	}
}

func TestBarkAndTelegramShapes(t *testing.T) {
	c := &capture{}
	srv := httptest.NewServer(c.handler())
	defer srv.Close()
	ev := Event{Kind: KindQuotaLow, Severity: SeverityWarning, Title: "额度告急", Body: "GoRouter 剩余 $0.30"}

	bark := NewBark(srv.URL+"/deviceKey", srv.Client())
	if err := bark.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	tg := NewTelegram("123:abc", "42", srv.Client())
	tg.apiBase = srv.URL
	if err := tg.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	got := c.all()
	if len(got) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(got))
	}
	if got[0].path != "/deviceKey" || !strings.Contains(got[0].body, `"title":"额度告急"`) || !strings.Contains(got[0].body, `"group":"relayhub"`) {
		t.Fatalf("bark request wrong: %+v", got[0])
	}
	if got[1].path != "/bot123:abc/sendMessage" || got[1].form["chat_id"] != "42" || !strings.Contains(got[1].form["text"], "额度告急") || !strings.Contains(got[1].form["text"], "$0.30") {
		t.Fatalf("telegram request wrong: %+v", got[1])
	}
}

func TestNonSuccessStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusForbidden) }))
	defer srv.Close()
	if err := NewWebhook(srv.URL, srv.Client()).Send(context.Background(), Event{Kind: KindBreakerOpen, Title: "x"}); err == nil {
		t.Fatal("403 must surface as an error")
	}
}

// The dispatcher fans out to every sink, dedupes by (kind, channel) within
// the cooldown, and never blocks callers on network I/O.
func TestDispatcherCooldownAndFanout(t *testing.T) {
	a, b := &capture{}, &capture{}
	sa, sb := httptest.NewServer(a.handler()), httptest.NewServer(b.handler())
	defer sa.Close()
	defer sb.Close()
	now := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	d := NewDispatcher(Options{Cooldown: 5 * time.Minute, Now: func() time.Time { return now }}, NewWebhook(sa.URL, sa.Client()), NewWebhook(sb.URL, sb.Client()))

	d.Notify(Event{Kind: KindCheckinFailed, ChannelID: "c1", Title: "1"})
	d.Notify(Event{Kind: KindCheckinFailed, ChannelID: "c1", Title: "dup"}) // suppressed
	d.Notify(Event{Kind: KindCheckinFailed, ChannelID: "c2", Title: "2"})   // other channel
	d.Notify(Event{Kind: KindNeedManual, ChannelID: "c1", Title: "3"})      // other kind
	now = now.Add(6 * time.Minute)
	d.Notify(Event{Kind: KindCheckinFailed, ChannelID: "c1", Title: "4"}) // cooldown elapsed
	d.Flush(5 * time.Second)

	for name, c := range map[string]*capture{"a": a, "b": b} {
		got := c.all()
		if len(got) != 4 {
			t.Fatalf("sink %s: expected 4 deliveries, got %d: %+v", name, len(got), got)
		}
		titles := ""
		for _, r := range got {
			var p map[string]any
			_ = json.Unmarshal([]byte(r.body), &p)
			titles += p["title"].(string)
		}
		if strings.Contains(titles, "dup") {
			t.Fatalf("sink %s: duplicate within cooldown must be suppressed: %s", name, titles)
		}
	}
	if d.Stats().Sent != 4 || d.Stats().Suppressed != 1 {
		t.Fatalf("stats: %+v", d.Stats())
	}
}

func TestDispatcherWithoutSinksIsNoop(t *testing.T) {
	var d *Dispatcher
	d.Notify(Event{Kind: KindBreakerOpen}) // nil-safe
	d = NewDispatcher(Options{})
	if d.Enabled() {
		t.Fatal("dispatcher without sinks must report disabled")
	}
	d.Notify(Event{Kind: KindBreakerOpen})
	d.Flush(time.Second)
}

func TestFromSettingsBuildsConfiguredSinks(t *testing.T) {
	d := FromSettings(Settings{WebhookURL: "https://hook.example/x", BarkURL: "https://api.day.app/key", TelegramBotToken: "1:a", TelegramChatID: "9"}, nil)
	if !d.Enabled() || len(d.Sinks()) != 3 {
		t.Fatalf("expected 3 sinks, got %v", d.Sinks())
	}
	d = FromSettings(Settings{TelegramBotToken: "1:a"}, nil) // chat id missing -> no telegram
	if d.Enabled() {
		t.Fatal("telegram without chat id must not be configured")
	}
	if names := FromSettings(Settings{BarkURL: "api.day.app/key"}, nil).Sinks(); len(names) != 0 {
		t.Fatalf("invalid bark url must be rejected, got %v", names)
	}
}

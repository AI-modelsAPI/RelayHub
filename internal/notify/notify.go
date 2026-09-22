// Package notify delivers operator-facing events (check-in failed, manual
// action needed, quota low, breaker open, balance refresh failed) to simple
// push sinks: generic Webhook, Bark and Telegram. It is intentionally small:
// no templates, no persistence, standard library only. Delivery is
// asynchronous and rate-limited per (kind, channel) so a flapping upstream
// cannot spam a phone.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	KindCheckinFailed  Kind = "checkin_failed"
	KindNeedManual     Kind = "checkin_need_manual"
	KindQuotaLow       Kind = "quota_low"
	KindQuotaExhausted Kind = "quota_exhausted"
	KindBreakerOpen    Kind = "circuit_open"
	KindAuthExpired    Kind = "auth_expired"
	KindBalanceFailed  Kind = "balance_refresh_failed"
	KindRecovered      Kind = "channel_recovered"
	KindTest           Kind = "test"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Event is one operator-facing notification.
type Event struct {
	Kind      Kind              `json:"kind"`
	Severity  Severity          `json:"severity"`
	Title     string            `json:"title"`
	Body      string            `json:"body,omitempty"`
	ChannelID string            `json:"channel_id,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	At        time.Time         `json:"at"`
}

// Sink delivers one event.
type Sink interface {
	Name() string
	Send(ctx context.Context, ev Event) error
}

// Settings mirrors the user-facing configuration (config.NotifyConfig).
type Settings struct {
	WebhookURL       string
	BarkURL          string
	TelegramBotToken string
	TelegramChatID   string
}

const (
	defaultTimeout  = 10 * time.Second
	defaultCooldown = 5 * time.Minute
	queueSize       = 256
)

// ---- Webhook -------------------------------------------------------------

type Webhook struct {
	URL    string
	client *http.Client
}

func NewWebhook(rawURL string, client *http.Client) *Webhook {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Webhook{URL: rawURL, client: client}
}

func (w *Webhook) Name() string { return "webhook" }

func (w *Webhook) Send(ctx context.Context, ev Event) error {
	payload := map[string]any{
		"source":     "relayhub",
		"kind":       ev.Kind,
		"severity":   ev.Severity,
		"title":      ev.Title,
		"body":       ev.Body,
		"channel_id": ev.ChannelID,
		"fields":     ev.Fields,
		"at":         ev.At.UTC().Format(time.RFC3339),
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "relayhub-notify/1")
	return do(w.client, req)
}

// ---- Bark ----------------------------------------------------------------

// Bark posts to https://api.day.app/<device_key> (or a self-hosted server).
type Bark struct {
	URL    string
	client *http.Client
}

func NewBark(rawURL string, client *http.Client) *Bark {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Bark{URL: strings.TrimRight(rawURL, "/"), client: client}
}

func (b *Bark) Name() string { return "bark" }

func (b *Bark) Send(ctx context.Context, ev Event) error {
	payload := map[string]any{
		"title": ev.Title,
		"body":  ev.Body,
		"group": "relayhub",
	}
	if ev.Severity == SeverityError {
		payload["level"] = "timeSensitive"
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.URL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	return do(b.client, req)
}

// ---- Telegram ------------------------------------------------------------

type Telegram struct {
	Token   string
	ChatID  string
	client  *http.Client
	apiBase string
}

func NewTelegram(token, chatID string, client *http.Client) *Telegram {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Telegram{Token: token, ChatID: chatID, client: client, apiBase: "https://api.telegram.org"}
}

func (t *Telegram) Name() string { return "telegram" }

func (t *Telegram) Send(ctx context.Context, ev Event) error {
	text := ev.Title
	if ev.Body != "" {
		text += "\n" + ev.Body
	}
	form := url.Values{"chat_id": {t.ChatID}, "text": {text}, "disable_web_page_preview": {"true"}}
	endpoint := strings.TrimRight(t.apiBase, "/") + "/bot" + t.Token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(t.client, req)
}

func do(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notify: %s responded %d", req.URL.Host, resp.StatusCode)
	}
	return nil
}

// ---- Dispatcher ----------------------------------------------------------

// Options tunes the dispatcher.
type Options struct {
	// Cooldown suppresses repeats of the same (kind, channel) pair. Default 5m.
	Cooldown time.Duration
	Now      func() time.Time
	// Logf receives delivery failures; nil discards them.
	Logf func(format string, args ...any)
}

// Stats counts dispatcher activity.
type Stats struct {
	Sent       int
	Suppressed int
	Failed     int
}

// Dispatcher fans events out to sinks asynchronously with per-key cooldown.
type Dispatcher struct {
	opts  Options
	sinks []Sink

	mu       sync.Mutex
	lastSent map[string]time.Time
	stats    Stats

	queue chan Event
	wg    sync.WaitGroup
	once  sync.Once
}

func NewDispatcher(opts Options, sinks ...Sink) *Dispatcher {
	if opts.Cooldown <= 0 {
		opts.Cooldown = defaultCooldown
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	d := &Dispatcher{opts: opts, sinks: sinks, lastSent: map[string]time.Time{}}
	if len(sinks) > 0 {
		d.queue = make(chan Event, queueSize)
		d.wg.Add(1)
		go d.loop()
	}
	return d
}

// FromSettings builds a dispatcher from user settings, skipping incomplete or
// malformed sinks (a Telegram token without a chat id, a Bark URL without a
// scheme) so a typo disables one sink rather than crashing startup.
func FromSettings(s Settings, logf func(string, ...any)) *Dispatcher {
	var sinks []Sink
	if u := strings.TrimSpace(s.WebhookURL); validHTTPURL(u) {
		sinks = append(sinks, NewWebhook(u, nil))
	}
	if u := strings.TrimSpace(s.BarkURL); validHTTPURL(u) {
		sinks = append(sinks, NewBark(u, nil))
	}
	if tok, chat := strings.TrimSpace(s.TelegramBotToken), strings.TrimSpace(s.TelegramChatID); tok != "" && chat != "" {
		sinks = append(sinks, NewTelegram(tok, chat, nil))
	}
	return NewDispatcher(Options{Logf: logf}, sinks...)
}

func validHTTPURL(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// Enabled reports whether at least one sink is configured.
func (d *Dispatcher) Enabled() bool { return d != nil && len(d.sinks) > 0 }

// Sinks lists configured sink names.
func (d *Dispatcher) Sinks() []string {
	if d == nil {
		return nil
	}
	out := make([]string, 0, len(d.sinks))
	for _, s := range d.sinks {
		out = append(out, s.Name())
	}
	return out
}

// Notify enqueues an event. It never blocks on network I/O and is safe on a
// nil dispatcher. Repeats of the same (kind, channel) within the cooldown are
// dropped.
func (d *Dispatcher) Notify(ev Event) {
	if !d.Enabled() {
		return
	}
	now := d.opts.Now()
	if ev.At.IsZero() {
		ev.At = now
	}
	if ev.Severity == "" {
		ev.Severity = SeverityWarning
	}
	key := string(ev.Kind) + "|" + ev.ChannelID
	d.mu.Lock()
	if last, ok := d.lastSent[key]; ok && now.Sub(last) < d.opts.Cooldown {
		d.stats.Suppressed++
		d.mu.Unlock()
		return
	}
	d.lastSent[key] = now
	d.mu.Unlock()
	select {
	case d.queue <- ev:
	default:
		d.mu.Lock()
		d.stats.Failed++
		d.mu.Unlock()
		if d.opts.Logf != nil {
			d.opts.Logf("notify: queue full, dropping %s for %s", ev.Kind, ev.ChannelID)
		}
	}
}

// Stats returns a snapshot of counters.
func (d *Dispatcher) Stats() Stats {
	if d == nil {
		return Stats{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stats
}

func (d *Dispatcher) loop() {
	defer d.wg.Done()
	for ev := range d.queue {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		var errs []error
		for _, s := range d.sinks {
			if err := s.Send(ctx, ev); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
			}
		}
		cancel()
		d.mu.Lock()
		if len(errs) == 0 {
			d.stats.Sent++
		} else {
			d.stats.Failed++
		}
		d.mu.Unlock()
		if len(errs) > 0 && d.opts.Logf != nil {
			d.opts.Logf("notify: delivery of %s failed: %v", ev.Kind, errors.Join(errs...))
		}
	}
}

// Flush stops accepting events and waits (bounded) for the queue to drain.
func (d *Dispatcher) Flush(timeout time.Duration) {
	if !d.Enabled() {
		return
	}
	d.once.Do(func() { close(d.queue) })
	done := make(chan struct{})
	go func() { d.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

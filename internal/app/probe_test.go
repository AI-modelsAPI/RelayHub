package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/gateway"
	"relayhub/internal/notify"
	"relayhub/internal/router"
	"relayhub/internal/verify"
)

func TestPickProbeBinding(t *testing.T) {
	mk := func(model, upstream string) router.Decision {
		pm := domain.ProviderModel{ID: "pm-" + model, ModelID: model, UpstreamModelName: upstream, Enabled: true}
		return router.Decision{Model: domain.Model{ID: model, Enabled: true}, ProviderModel: pm, Channel: domain.Channel{ID: "c"}}
	}
	pick := func(model string, recent []string, defaultTest string) string {
		bindings := []router.Decision{mk("a", "up-a"), mk("b", "up-b"), mk("c", "up-c")}
		for i := range bindings {
			bindings[i].Channel.DefaultTestModel = defaultTest
		}
		d, ok := pickProbeBinding(bindings, model, recent)
		if !ok {
			return ""
		}
		return d.Model.ID
	}
	for _, c := range []struct {
		name, model string
		recent      []string
		defaultTest string
		want        string
	}{
		{"explicit model", "b", nil, "", "b"},
		{"explicit upstream name", "up-c", nil, "", "c"},
		{"explicit but not served", "zzz", nil, "", ""},
		{"channel default test model", "", nil, "c", "c"},
		{"default test model by upstream name", "", nil, "up-b", "b"},
		{"most used recently", "", []string{"b", "c", "b", "a"}, "", "b"},
		{"most used that is still bound", "", []string{"gone", "gone", "c"}, "", "c"},
		{"tie goes to the most recent", "", []string{"c", "a"}, "", "c"},
		{"first binding", "", nil, "", "a"},
		{"stale default test model", "", nil, "nope", "a"},
	} {
		if got := pick(c.model, c.recent, c.defaultTest); got != c.want {
			t.Errorf("%s: picked %q, want %q", c.name, got, c.want)
		}
	}
	if _, ok := pickProbeBinding(nil, "", nil); ok {
		t.Fatal("no bindings, nothing to probe")
	}
}

func TestRunProbeLoopRepeatsUntilCancelled(t *testing.T) {
	var passes atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runProbeLoop(ctx, 5*time.Millisecond, time.Millisecond, func(context.Context) { passes.Add(1) })
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for passes.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("probe loop ignored cancellation")
	}
	if n := passes.Load(); n < 3 {
		t.Fatalf("probe loop ran %d passes, want at least 3", n)
	}

	called := false
	runProbeLoop(context.Background(), 0, 0, func(context.Context) { called = true })
	if called {
		t.Fatal("interval 0 must disable periodic probing")
	}
}

type cannedUpstream struct {
	mu    sync.Mutex
	calls int
	body  string
}

func (u *cannedUpstream) Do(_ context.Context, _ gateway.Request) (gateway.Response, error) {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	return gateway.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(u.body))}, nil
}

func (u *cannedUpstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

type fakeProbeRepo struct {
	channels []domain.Channel
	records  map[string][]domain.RequestRecord
}

func (f fakeProbeRepo) ListChannels(context.Context, string) ([]domain.Channel, error) {
	return f.channels, nil
}

func (f fakeProbeRepo) ListRequestRecordsByChannel(_ context.Context, channelID string, _ int) ([]domain.RequestRecord, error) {
	return f.records[channelID], nil
}

func TestChannelProberRecordsVerdictAndNotifiesOnTransition(t *testing.T) {
	channels := map[string]domain.Channel{
		"c": {ID: "c", ProviderID: "p", Name: "Relay C", BaseURL: "http://c.invalid", Enabled: true, RoutingEnabled: true},
		"d": {ID: "d", ProviderID: "p", Name: "Relay D", BaseURL: "http://d.invalid", Enabled: true, RoutingEnabled: false},
	}
	res := &router.Resolver{}
	res.UpdateSnapshot(nil, channels,
		map[string]domain.Model{"m": {ID: "m", Enabled: true}},
		[]domain.ProviderModel{
			{ID: "pm-c", ProviderID: "p", ChannelID: "c", ModelID: "m", UpstreamModelName: "claude-sonnet-4-5", Protocol: "anthropic-messages", Enabled: true},
			{ID: "pm-d", ProviderID: "p", ChannelID: "d", ModelID: "m", UpstreamModelName: "claude-sonnet-4-5", Protocol: "anthropic-messages", Enabled: true},
		}, nil, nil, nil)
	// A relay answering every request with a cheap model and a canned text.
	up := &cannedUpstream{body: `{"model":"glm-4.6","content":[{"type":"text","text":"Hello there"}],"stop_reason":"end_turn","usage":{"input_tokens":20}}`}
	reg := verify.New()
	var events []notify.Event
	p := &channelProber{
		resolver: res,
		prober:   gateway.Prober{Upstream: up},
		verify:   reg,
		repo:     fakeProbeRepo{channels: []domain.Channel{channels["c"], channels["d"]}},
		notify:   func(ev notify.Event) { events = append(events, ev) },
	}
	ctx := context.Background()

	got, err := p.probe(ctx, "c", "")
	if err != nil || !got.Conclusive || got.Score >= verify.SuspectBelow || got.ModelID != "m" {
		t.Fatalf("probe: %v %+v", err, got)
	}
	if bad, _ := reg.Suspect("c"); !bad {
		t.Fatal("verdict was not recorded in the registry")
	}
	if len(events) != 1 || events[0].Kind != notify.KindAuthenticitySuspect || events[0].ChannelID != "c" || !strings.Contains(events[0].Title, "Relay C") {
		t.Fatalf("expected one authenticity notification, got %+v", events)
	}
	if _, err := p.probe(ctx, "c", "m"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("a channel that stays suspect must not re-notify: %+v", events)
	}
	if _, err := p.probe(ctx, "c", "nope"); !errors.Is(err, verify.ErrNoProbeTarget) {
		t.Fatalf("unserved model: %v", err)
	}
	if _, err := p.probe(ctx, "ghost", ""); !errors.Is(err, verify.ErrNoProbeTarget) {
		t.Fatalf("unknown channel: %v", err)
	}

	// A scheduled pass only spends requests on channels that carry traffic.
	before := up.count()
	p.pass(ctx)
	if n := up.count() - before; n != 1 {
		t.Fatalf("pass sent %d probe requests, want 1 (channel d is not routable)", n)
	}
}

func TestFullStackWiresAuthenticityProbes(t *testing.T) {
	a, err := New(Config{DataDir: t.TempDir(), HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		_ = a.Shutdown(sctx)
	}()
	rt := a.Runtime()
	if rt.Verify == nil || rt.Router.Trust == nil {
		t.Fatal("routing must consult the verify registry")
	}
	resp, err := http.Post("http://"+rt.APIListener.Addr().String()+"/api/v1/verify/probe", "application/json", strings.NewReader(`{"channel_id":"ghost"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("probe of an unknown channel: %d, want 404 from the wired prober", resp.StatusCode)
	}
}

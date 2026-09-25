package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"relayhub/internal/adapter"
	"relayhub/internal/billing"
	"relayhub/internal/domain"
	"relayhub/internal/notify"
	"relayhub/internal/repository"
)

// Billing reconciliation pass (AUDIT §5 B2): build the local ledger from the
// gateway's own request records, read the site's consumption log, record the
// verdict, notify on transitions and feed the router.

// reconcileNow is 20:00 in the billing timezone (Asia/Shanghai), so "today"
// started 2026-09-24T16:00:00Z and the window is unambiguous.
func reconcileNow() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) }

type fakeReconcileRepo struct {
	channels       []domain.Channel
	providers      map[string]domain.Provider
	models         []domain.Model
	providerModels []domain.ProviderModel
	records        map[string][]domain.RequestRecord
	reports        []domain.BillingReport
}

func (f *fakeReconcileRepo) ListChannels(context.Context, string) ([]domain.Channel, error) {
	return f.channels, nil
}

func (f *fakeReconcileRepo) GetChannel(_ context.Context, id string) (domain.Channel, error) {
	for _, ch := range f.channels {
		if ch.ID == id {
			return ch, nil
		}
	}
	return domain.Channel{}, repository.ErrNotFound
}

func (f *fakeReconcileRepo) GetProvider(_ context.Context, id string) (domain.Provider, error) {
	p, ok := f.providers[id]
	if !ok {
		return domain.Provider{}, repository.ErrNotFound
	}
	return p, nil
}

func (f *fakeReconcileRepo) ListModels(context.Context) ([]domain.Model, error) {
	return f.models, nil
}

func (f *fakeReconcileRepo) ListProviderModels(context.Context, string) ([]domain.ProviderModel, error) {
	return f.providerModels, nil
}

func (f *fakeReconcileRepo) ListRequestRecordsByChannel(_ context.Context, channelID string, limit int) ([]domain.RequestRecord, error) {
	rs := f.records[channelID]
	if limit > 0 && len(rs) > limit {
		rs = rs[:limit]
	}
	return rs, nil
}

func (f *fakeReconcileRepo) CreateBillingReport(_ context.Context, r domain.BillingReport) error {
	f.reports = append(f.reports, r)
	return nil
}

func (f *fakeReconcileRepo) ListBillingReports(_ context.Context, channelID string, limit int) ([]domain.BillingReport, error) {
	var out []domain.BillingReport
	for _, r := range f.reports {
		if channelID == "" || r.ChannelID == channelID {
			out = append(out, r)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// fakeUsageAdapter implements the optional UsageReporter on top of the shared
// new-api adapter.
type fakeUsageAdapter struct {
	adapter.BaseNewAPIAdapter
	calls   int
	queries []adapter.UsageLogQuery
	result  adapter.UsageLogResult
	err     error
}

func (f *fakeUsageAdapter) UsageLog(_ context.Context, _ domain.Channel, q adapter.UsageLogQuery) (adapter.UsageLogResult, error) {
	f.calls++
	f.queries = append(f.queries, q)
	return f.result, f.err
}

// plainAdapter implements ProviderAdapter but not UsageReporter: a site whose
// consumption log we cannot read must be skipped, not failed.
type plainAdapter struct{ adapter.ProviderAdapter }

func testChannels() ([]domain.Channel, map[string]domain.Provider) {
	channels := []domain.Channel{
		{ID: "c1", ProviderID: "p", Name: "Relay C", Enabled: true, RoutingEnabled: true},
		{ID: "c2", ProviderID: "p", Name: "Relay D", Enabled: true, RoutingEnabled: true},
		{ID: "c3", ProviderID: "p", Name: "Paused", Enabled: false, RoutingEnabled: false},
	}
	providers := map[string]domain.Provider{"p": {ID: "p", Name: "P", AdapterType: "generic", Enabled: true}}
	return channels, providers
}

func testRecords(now time.Time) map[string][]domain.RequestRecord {
	return map[string][]domain.RequestRecord{
		"c1": {
			{ID: "r2", ModelID: "m", InputTokens: 600_000, CreatedAt: now.Add(-time.Hour)},
			{ID: "r1", ModelID: "m", InputTokens: 400_000, CreatedAt: now.Add(-2 * time.Hour)},
			// Yesterday: outside the billing day and must not count.
			{ID: "old", ModelID: "m", InputTokens: 9_000_000, CreatedAt: now.Add(-30 * time.Hour)},
		},
		"c2": {{ID: "r3", ModelID: "m", InputTokens: 500_000, CreatedAt: now.Add(-time.Hour)}},
	}
}

func newTestReconciler(repo reconcileRepo, up adapter.ProviderAdapter, events *[]notify.Event, now time.Time) *channelReconciler {
	reg := adapter.NewRegistry()
	_ = reg.Register("generic", up)
	return &channelReconciler{
		repo:       repo,
		adapters:   reg,
		reports:    billing.NewRegistry(),
		notify:     func(ev notify.Event) { *events = append(*events, ev) },
		logf:       func(string, ...any) {},
		now:        func() time.Time { return now },
		threshold:  DefaultReconcileThreshold,
		minTokens:  DefaultReconcileMinTokens,
		maxEntries: 25,
		spacing:    0,
		source:     "reconcile",
	}
}

func TestReconcileChannelBuildsLedgerFromTodaysRecords(t *testing.T) {
	now := reconcileNow()
	channels, providers := testChannels()
	repo := &fakeReconcileRepo{
		channels:  channels,
		providers: providers,
		models:    []domain.Model{{ID: "m", Enabled: true, InputPrice: 1.0}},
		providerModels: []domain.ProviderModel{
			{ID: "pm", ProviderID: "p", ChannelID: "c1", ModelID: "m", UpstreamModelName: "claude-x", Enabled: true},
		},
		records: testRecords(now),
	}
	up := &fakeUsageAdapter{result: adapter.UsageLogResult{
		QuotaPerUnit: 500000,
		Total:        1,
		Entries: []adapter.UsageLogEntry{{
			CreatedAt: now.Add(-time.Hour), ModelName: "claude-x", Quota: 4_000_000,
			PromptTokens: 1_000_000, Multipliers: domain.Multipliers{GroupRatio: 1, Known: true},
		}},
	}}
	var events []notify.Event
	r := newTestReconciler(repo, up, &events, now)

	rep, err := r.reconcileChannel(context.Background(), channels[0])
	if err != nil {
		t.Fatal(err)
	}
	wantSince := time.Date(2026, 9, 25, 0, 0, 0, 0, billingZone) // Beijing midnight = 2026-09-24 16:00Z
	if rep.Requests != 2 || rep.Tokens != 1_000_000 || !rep.Window.Since.Equal(wantSince) {
		t.Fatalf("ledger window=%+v requests=%d tokens=%d", rep.Window, rep.Requests, rep.Tokens)
	}
	if rep.MatchedModels != 1 {
		t.Fatalf("matched models = %d; the site's model name must be matched to the binding", rep.MatchedModels)
	}
	// $1/1M in the catalog versus $8/1M charged.
	if rep.Verdict != billing.VerdictOvercharge || rep.ChargedUSD != 8 {
		t.Fatalf("verdict=%q charged=$%v reason=%q", rep.Verdict, rep.ChargedUSD, rep.Reason)
	}
	if len(up.queries) != 1 || up.queries[0].MaxEntries != 25 || !up.queries[0].Since.Equal(wantSince) {
		t.Fatalf("usage log query = %+v", up.queries)
	}
	if len(repo.reports) != 1 {
		t.Fatalf("persisted reports = %d", len(repo.reports))
	}
	row := repo.reports[0]
	if row.ChannelID != "c1" || row.Source != "reconcile" || row.Verdict != billing.VerdictOvercharge ||
		row.Tokens != 1_000_000 || row.Entries != 1 || !row.DeclaredKnown {
		t.Fatalf("persisted row = %+v", row)
	}
	if price, ok := r.reports.EffectiveUSDPerMTok("c1"); !ok || price != 8 {
		t.Fatalf("router price = %v (%v)", price, ok)
	}
	if len(events) != 1 {
		t.Fatalf("notifications = %+v", events)
	}
	ev := events[0]
	if ev.Kind != notify.KindBillingDrift || ev.ChannelID != "c1" || !strings.Contains(ev.Title, "计费异常") {
		t.Fatalf("event = %+v", ev)
	}
	if !strings.Contains(ev.Body, "目录价") {
		t.Fatalf("event body must carry the reason: %q", ev.Body)
	}

	// A second pass with the same finding reports it again but must not
	// notify twice: only transitions are announced.
	if _, err := r.reconcileChannel(context.Background(), channels[0]); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || len(repo.reports) != 2 {
		t.Fatalf("events=%d reports=%d", len(events), len(repo.reports))
	}
}

func TestReconcileChannelUsesTheLastAcceptedReportAsBaseline(t *testing.T) {
	now := reconcileNow()
	channels, providers := testChannels()
	// The catalog has no price for the model; yesterday's report was ok at
	// $2/1M, which is the reference the new window is compared against.
	repo := &fakeReconcileRepo{
		channels:  channels,
		providers: providers,
		models:    []domain.Model{{ID: "m", Enabled: true}},
		providerModels: []domain.ProviderModel{
			{ID: "pm", ProviderID: "p", ChannelID: "c1", ModelID: "m", UpstreamModelName: "m", Enabled: true},
		},
		records: testRecords(now),
		reports: []domain.BillingReport{{
			ID: "old", ChannelID: "c1", Verdict: billing.VerdictOK, Tokens: 1_000_000,
			EffectiveUSDPerMTok: 2, GroupRatio: 1, CreatedAt: now.Add(-24 * time.Hour),
		}},
	}
	up := &fakeUsageAdapter{result: adapter.UsageLogResult{
		QuotaPerUnit: 500000,
		Total:        1,
		Entries: []adapter.UsageLogEntry{{
			CreatedAt: now.Add(-time.Hour), ModelName: "m", Quota: 4_000_000, PromptTokens: 1_000_000,
			Multipliers: domain.Multipliers{GroupRatio: 1, Known: true},
		}},
	}}
	var events []notify.Event
	r := newTestReconciler(repo, up, &events, now)

	rep, err := r.reconcileChannel(context.Background(), channels[0])
	if err != nil {
		t.Fatal(err)
	}
	if rep.BaselineUSDPerMTok != 2 || rep.Verdict != billing.VerdictDrift {
		t.Fatalf("baseline=%v verdict=%q reason=%q", rep.BaselineUSDPerMTok, rep.Verdict, rep.Reason)
	}
	if !strings.Contains(rep.Reason, "2.0000") {
		t.Fatalf("reason must name the previous price: %q", rep.Reason)
	}
	if len(events) != 1 || events[0].Kind != notify.KindBillingDrift {
		t.Fatalf("events = %+v", events)
	}
}

func TestReconcilePassSkipsChannelsWithoutASiteLog(t *testing.T) {
	now := reconcileNow()
	channels, providers := testChannels()
	repo := &fakeReconcileRepo{channels: channels, providers: providers, records: testRecords(now)}
	var events []notify.Event
	r := newTestReconciler(repo, plainAdapter{}, &events, now)

	r.pass(context.Background())

	if len(repo.reports) != 0 || len(events) != 0 {
		t.Fatalf("reports=%d events=%d", len(repo.reports), len(events))
	}
	if _, err := r.reconcileChannel(context.Background(), channels[0]); !errors.Is(err, errNoUsageLog) {
		t.Fatalf("err = %v; want the capability gap", err)
	}
}

func TestReconcileRunCoversEveryEnabledChannelOrOne(t *testing.T) {
	now := reconcileNow()
	channels, providers := testChannels()
	repo := &fakeReconcileRepo{
		channels: channels, providers: providers,
		models: []domain.Model{{ID: "m", Enabled: true}},
		providerModels: []domain.ProviderModel{
			{ID: "pm", ProviderID: "p", ModelID: "m", UpstreamModelName: "m", Enabled: true},
		},
		records: testRecords(now),
	}
	up := &fakeUsageAdapter{result: adapter.UsageLogResult{
		QuotaPerUnit: 500000, Total: 1,
		Entries: []adapter.UsageLogEntry{{CreatedAt: now.Add(-time.Hour), ModelName: "m", Quota: 500_000, PromptTokens: 1_000_000}},
	}}
	var events []notify.Event
	r := newTestReconciler(repo, up, &events, now)

	all, err := r.run(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("reports = %d; a disabled channel must be skipped", len(all))
	}
	if _, err := r.run(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(context.Background(), "ghost"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("unknown channel err = %v", err)
	}
}

func TestFullStackWiresBillingReconciliation(t *testing.T) {
	start := func(t *testing.T, interval time.Duration) *Runtime {
		t.Helper()
		a, err := New(Config{DataDir: t.TempDir(), HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true, BillingReconcileInterval: interval})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if err := a.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cancel()
			sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer scancel()
			_ = a.Shutdown(sctx)
		})
		return a.Runtime()
	}

	rt := start(t, time.Hour)
	if rt.Billing == nil || rt.reconcilePass == nil || rt.reconcileEvery != time.Hour {
		t.Fatalf("the reconciliation loop must be wired: %+v", rt.reconcileEvery)
	}
	if rt.APIServer.Billing != rt.Billing || rt.APIServer.Reconciler == nil {
		t.Fatal("the management API must expose the registry and the on-demand pass")
	}
	if rt.Router.Price == nil {
		t.Fatal("the router must rank channels by measured price")
	}
	rt.Billing.Record(billing.Report{ChannelID: "c", Tokens: 1_000_000, EffectiveUSDPerMTok: 2})
	if price, ok := rt.Router.Price.EffectiveUSDPerMTok("c"); !ok || price != 2 {
		t.Fatalf("router price = %v (%v)", price, ok)
	}

	off := start(t, 0)
	if off.reconcileEvery != 0 || off.reconcilePass == nil {
		t.Fatal("interval 0 must keep the periodic pass off; the endpoint still reconciles on demand")
	}
}

package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"relayhub/internal/adapter"
	"relayhub/internal/billing"
	"relayhub/internal/domain"
	"relayhub/internal/notify"
)

// Billing reconciliation (AUDIT §5 B2): what the relay charged versus what
// RelayHub counted, per channel, once a (billing) day.

const (
	// reconcileInitialDelay lets startup finish (catalog load, first requests)
	// before the first pass; the pass then repeats every configured interval.
	reconcileInitialDelay = 3 * time.Minute
	// reconcileRecordLimit bounds the local history read per channel.
	reconcileRecordLimit = 5000
	// reconcileHistoryLimit is how many past reports feed the baseline.
	reconcileHistoryLimit = 100
	// reconcileSpacing pauses between channels so a pass is not a burst of
	// account-endpoint calls.
	reconcileSpacing = 1 * time.Second
	// reconcileTimeout bounds one channel's pass.
	reconcileTimeout = 60 * time.Second
	// DefaultReconcileThreshold is the relative deviation that turns a
	// window into a finding (15%): above the rounding noise of real relays,
	// below the multipliers sites actually move.
	DefaultReconcileThreshold = 0.15
	// DefaultReconcileMinTokens keeps idle windows from producing verdicts.
	DefaultReconcileMinTokens = 1000
	// DefaultReconcileMaxEntries bounds one site log read.
	DefaultReconcileMaxEntries = 1000
)

// billingZone is the billing timezone of new-api family sites (Asia/Shanghai):
// their consumption logs and daily statistics are bucketed by this day, so the
// reconciliation window has to be the same day the site is charging in.
var billingZone = time.FixedZone("Asia/Shanghai", 8*3600)

// errNoUsageLog marks adapters that cannot read a site-side usage log; it is
// an expected capability gap, not a failure worth logging every pass.
var errNoUsageLog = errors.New("adapter cannot read the site usage log")

// reconcileRepo is the slice of the repository the reconciler reads and
// writes. Kept narrow so tests can drive the pass with fakes.
type reconcileRepo interface {
	ListChannels(ctx context.Context, providerID string) ([]domain.Channel, error)
	GetChannel(ctx context.Context, id string) (domain.Channel, error)
	GetProvider(ctx context.Context, id string) (domain.Provider, error)
	ListModels(ctx context.Context) ([]domain.Model, error)
	ListProviderModels(ctx context.Context, modelID string) ([]domain.ProviderModel, error)
	ListRequestRecordsByChannel(ctx context.Context, channelID string, limit int) ([]domain.RequestRecord, error)
	CreateBillingReport(ctx context.Context, r domain.BillingReport) error
	ListBillingReports(ctx context.Context, channelID string, limit int) ([]domain.BillingReport, error)
}

type channelReconciler struct {
	repo     reconcileRepo
	adapters *adapter.Registry
	reports  *billing.Registry
	notify   func(notify.Event)
	logf     func(string, ...any)
	// now is the clock (tests); nil means time.Now.
	now func() time.Time
	// threshold, minTokens and maxEntries mirror billing.Params plus the
	// per-read entry cap; zero means the defaults above.
	threshold  float64
	minTokens  int64
	maxEntries int
	spacing    time.Duration
	// source labels who ran a pass ("reconcile" or "manual").
	source string
}

// billingWindow is the reconciliation window: the site's current billing day
// up to now.
func billingWindow(now time.Time) billing.Window {
	local := now.In(billingZone)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, billingZone)
	return billing.Window{Since: start, Until: now}
}

func (r *channelReconciler) params() billing.Params {
	p := billing.Params{Threshold: r.threshold, MinTokens: r.minTokens}
	return p
}

func (r *channelReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *channelReconciler) log(format string, args ...any) {
	if r.logf != nil {
		r.logf(format, args...)
	}
}

// pass reconciles every enabled channel that has a provider, one after the
// other with a short pause, so the site log endpoints see a steady trickle.
func (r *channelReconciler) pass(ctx context.Context) {
	if r.repo == nil || r.adapters == nil {
		return
	}
	channels, err := r.repo.ListChannels(ctx, "")
	if err != nil {
		r.log("relayhub: billing reconcile: list channels: %v", err)
		return
	}
	for _, ch := range channels {
		if ctx.Err() != nil {
			return
		}
		if !ch.Enabled || ch.ProviderID == "" {
			continue
		}
		if _, err := r.reconcileChannel(ctx, ch); err != nil {
			if !errors.Is(err, errNoUsageLog) && !errors.Is(err, adapter.ErrUnsupportedOperation) {
				r.log("relayhub: billing reconcile: channel %s: %v", ch.ID, err)
			}
		}
		if r.spacing > 0 {
			t := time.NewTimer(r.spacing)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}
}

// run reconciles one channel (channelID != "") or every enabled channel and
// returns the reports it produced. It is the manual entry point behind
// POST /api/v1/billing/reconcile.
func (r *channelReconciler) run(ctx context.Context, channelID string) ([]billing.Report, error) {
	if channelID != "" {
		ch, err := r.repo.GetChannel(ctx, channelID)
		if err != nil {
			return nil, err
		}
		rep, err := r.reconcileChannel(ctx, ch)
		if err != nil {
			return nil, err
		}
		return []billing.Report{rep}, nil
	}
	channels, err := r.repo.ListChannels(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]billing.Report, 0, len(channels))
	var firstErr error
	for _, ch := range channels {
		if ctx.Err() != nil {
			break
		}
		if !ch.Enabled || ch.ProviderID == "" {
			continue
		}
		rep, err := r.reconcileChannel(ctx, ch)
		if err != nil {
			if firstErr == nil && !errors.Is(err, errNoUsageLog) && !errors.Is(err, adapter.ErrUnsupportedOperation) {
				firstErr = err
			}
			continue
		}
		out = append(out, rep)
	}
	return out, firstErr
}

// reconcileChannel reads one channel's local ledger and the site's charges,
// reconciles them, records the report (memory first, then disk) and notifies
// on a transition into a finding.
func (r *channelReconciler) reconcileChannel(ctx context.Context, ch domain.Channel) (billing.Report, error) {
	provider, err := r.repo.GetProvider(ctx, ch.ProviderID)
	if err != nil {
		return billing.Report{}, err
	}
	adp, err := r.adapters.Resolve(provider)
	if err != nil {
		return billing.Report{}, err
	}
	reporter, ok := adp.(adapter.UsageReporter)
	if !ok {
		return billing.Report{}, fmt.Errorf("%w: %s (%s)", errNoUsageLog, provider.Name, provider.AdapterType)
	}
	now := r.clock()
	window := billingWindow(now)
	cctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()

	ledger, err := r.ledger(cctx, ch, window)
	if err != nil {
		return billing.Report{}, err
	}
	usage, err := reporter.UsageLog(cctx, ch, adapter.UsageLogQuery{
		Since: window.Since, Until: window.Until, MaxEntries: r.maxEntries,
	})
	if err != nil {
		return billing.Report{}, err
	}
	base := billing.BaselineOf(r.observations(cctx, ch.ID), r.params())
	site := billing.Charges{Entries: chargesFrom(usage), Truncated: usage.Truncated}
	rep := billing.Reconcile(ledger, site, base, r.params(), now)

	prev, had := r.reports.Record(rep)
	if err := r.repo.CreateBillingReport(ctx, reportRecord(rep, r.source)); err != nil {
		r.log("relayhub: billing reconcile: persist report for channel %s: %v", ch.ID, err)
	}
	if rep.Bad() {
		r.log("relayhub: billing reconcile: channel %s %s: %s", ch.ID, rep.Verdict, rep.Reason)
		if (!had || !prev.Bad()) && r.notify != nil {
			r.notify(notify.Event{
				Kind:      notify.KindBillingDrift,
				Severity:  notify.SeverityWarning,
				Title:     "计费异常：" + channelLabel(ch),
				Body:      rep.Reason + "\n" + moneyLine(rep),
				ChannelID: ch.ID,
				Fields: map[string]string{
					"verdict":                rep.Verdict,
					"effective_usd_per_mtok": fmt.Sprintf("%.4f", rep.EffectiveUSDPerMTok),
					"charged_usd":            fmt.Sprintf("%.4f", rep.ChargedUSD),
				},
				At: rep.CreatedAt,
			})
		}
	}
	return rep, nil
}

// ledger builds the local side of the comparison from the gateway's own
// request records in the window, priced with the catalog's list prices.
func (r *channelReconciler) ledger(ctx context.Context, ch domain.Channel, window billing.Window) (billing.Ledger, error) {
	records, err := r.repo.ListRequestRecordsByChannel(ctx, ch.ID, reconcileRecordLimit)
	if err != nil {
		return billing.Ledger{}, err
	}
	models, err := r.repo.ListModels(ctx)
	if err != nil {
		return billing.Ledger{}, err
	}
	prices := make(map[string]domain.Model, len(models))
	for _, m := range models {
		prices[m.ID] = m
	}
	upstream, err := r.upstreamNames(ctx, ch)
	if err != nil {
		return billing.Ledger{}, err
	}
	ledger := billing.Ledger{ChannelID: ch.ID, Window: window}
	byModel := map[string]*billing.ModelUsage{}
	for _, rec := range records {
		if rec.CreatedAt.Before(window.Since) || rec.CreatedAt.After(window.Until) {
			continue
		}
		ledger.Requests++
		ledger.InputTokens += int64(rec.InputTokens)
		ledger.OutputTokens += int64(rec.OutputTokens)
		ledger.CacheReadTokens += int64(rec.CacheReadTokens)
		id := rec.ModelID
		if id == "" {
			id = "unknown"
		}
		mu, ok := byModel[id]
		if !ok {
			mu = &billing.ModelUsage{ModelID: id, UpstreamName: upstream[id]}
			if m, known := prices[id]; known {
				mu.InputPrice = m.InputPrice
				mu.OutputPrice = m.OutputPrice
			}
			byModel[id] = mu
		}
		mu.Requests++
		mu.InputTokens += int64(rec.InputTokens)
		mu.OutputTokens += int64(rec.OutputTokens)
		mu.CacheReadTokens += int64(rec.CacheReadTokens)
	}
	ids := make([]string, 0, len(byModel))
	for id := range byModel {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ledger.Models = append(ledger.Models, *byModel[id])
	}
	return ledger, nil
}

// upstreamNames maps logical model IDs to the name this channel calls them by
// (channel-specific bindings win over provider-wide ones, then priority, then
// ID), so site log entries can be matched to models.
func (r *channelReconciler) upstreamNames(ctx context.Context, ch domain.Channel) (map[string]string, error) {
	bindings, err := r.repo.ListProviderModels(ctx, "")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	best := map[string]domain.ProviderModel{}
	better := func(a, b domain.ProviderModel) bool {
		if (a.ChannelID != "") != (b.ChannelID != "") {
			return a.ChannelID != ""
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	}
	for _, pm := range bindings {
		if pm.ChannelID != "" && pm.ChannelID != ch.ID {
			continue
		}
		if pm.ChannelID == "" && pm.ProviderID != ch.ProviderID {
			continue
		}
		if pm.UpstreamModelName == "" {
			continue
		}
		if cur, ok := best[pm.ModelID]; ok && !better(pm, cur) {
			continue
		}
		best[pm.ModelID] = pm
	}
	for id, pm := range best {
		out[id] = pm.UpstreamModelName
	}
	return out, nil
}

// observations converts stored reports into the shape the baseline picks from.
func (r *channelReconciler) observations(ctx context.Context, channelID string) []billing.Observation {
	rows, err := r.repo.ListBillingReports(ctx, channelID, reconcileHistoryLimit)
	if err != nil {
		return nil
	}
	out := make([]billing.Observation, 0, len(rows))
	for _, row := range rows {
		out = append(out, billing.Observation{
			At:                  row.CreatedAt,
			Verdict:             row.Verdict,
			Tokens:              row.Tokens,
			EffectiveUSDPerMTok: row.EffectiveUSDPerMTok,
			Multipliers:         domain.Multipliers{GroupRatio: row.GroupRatio, ModelRatio: row.ModelRatio, Known: row.GroupRatio > 0 || row.ModelRatio > 0},
		})
	}
	return out
}

// chargesFrom adapts site log entries to the reconciliation core's input.
func chargesFrom(usage adapter.UsageLogResult) []billing.Charge {
	out := make([]billing.Charge, 0, len(usage.Entries))
	for _, e := range usage.Entries {
		out = append(out, billing.Charge{
			CreatedAt:        e.CreatedAt,
			ModelName:        e.ModelName,
			Quota:            e.Quota,
			QuotaPerUnit:     usage.QuotaPerUnit,
			PromptTokens:     e.PromptTokens,
			CompletionTokens: e.CompletionTokens,
			Multipliers:      e.Multipliers,
		})
	}
	return out
}

// reportRecord maps a report onto its persisted form.
func reportRecord(rep billing.Report, source string) domain.BillingReport {
	return domain.BillingReport{
		ID:                  uuid.NewString(),
		ChannelID:           rep.ChannelID,
		WindowStart:         rep.Window.Since,
		WindowEnd:           rep.Window.Until,
		Source:              source,
		Requests:            rep.Requests,
		InputTokens:         rep.InputTokens,
		OutputTokens:        rep.OutputTokens,
		CacheReadTokens:     rep.CacheReadTokens,
		Tokens:              rep.Tokens,
		ChargedTokens:       rep.ChargedTokens,
		ChargedQuota:        rep.ChargedQuota,
		ChargedUSD:          rep.ChargedUSD,
		DeclaredUSD:         rep.DeclaredUSD,
		DeclaredKnown:       rep.DeclaredKnown,
		EffectiveUSDPerMTok: rep.EffectiveUSDPerMTok,
		DeclaredUSDPerMTok:  rep.DeclaredUSDPerMTok,
		BaselineUSDPerMTok:  rep.BaselineUSDPerMTok,
		Drift:               rep.Drift,
		TokenDrift:          rep.TokenDrift,
		MultiplierDrift:     rep.MultiplierDrift,
		SavingsUSD:          rep.SavingsUSD,
		GroupRatio:          rep.Multipliers.GroupRatio,
		ModelRatio:          rep.Multipliers.ModelRatio,
		Entries:             rep.Entries,
		MatchedModels:       rep.MatchedModels,
		Truncated:           rep.Truncated,
		Verdict:             rep.Verdict,
		Reason:              rep.Reason,
		CreatedAt:           rep.CreatedAt,
	}
}

// moneyLine is the one-line money summary appended to notifications.
func moneyLine(rep billing.Report) string {
	line := fmt.Sprintf("今日已付 $%.4f，本地记账 %d token", rep.ChargedUSD, rep.Tokens)
	if rep.DeclaredKnown {
		line += fmt.Sprintf("（按目录价应付 $%.4f）", rep.DeclaredUSD)
	}
	if rep.SavingsUSD > 0 {
		line += fmt.Sprintf("，省下 $%.4f", rep.SavingsUSD)
	}
	return line
}

// channelLabel is the human name of a channel in notifications.
func channelLabel(ch domain.Channel) string {
	if ch.Name != "" {
		return ch.Name
	}
	return ch.ID
}

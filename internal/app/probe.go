package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/gateway"
	"relayhub/internal/health"
	"relayhub/internal/notify"
	"relayhub/internal/router"
	"relayhub/internal/verify"
)

// Authenticity probes (AUDIT §5 B1): the glue between the routing snapshot,
// the gateway prober and the verify registry, plus the periodic pass.

// probeInitialDelay lets a fresh start settle (catalog load, check-ins)
// before the first pass; the pass then repeats every configured interval.
const probeInitialDelay = 2 * time.Minute

// probeRecentRecords bounds the usage history used to pick a channel's most
// used model.
const probeRecentRecords = 200

// probeRepo is the slice of the repository the prober reads.
type probeRepo interface {
	ListChannels(ctx context.Context, providerID string) ([]domain.Channel, error)
	ListRequestRecordsByChannel(ctx context.Context, channelID string, limit int) ([]domain.RequestRecord, error)
}

type channelProber struct {
	resolver *router.Resolver
	prober   gateway.Prober
	verify   *verify.Registry
	repo     probeRepo
	health   *health.Registry
	notify   func(notify.Event)
	logf     func(string, ...any)
	// spacing pauses between channels in a pass so probes never burst.
	spacing time.Duration
	// now is the clock (tests); nil means time.Now.
	now func() time.Time
}

// probe runs one probe against channelID's binding of modelID (empty: the
// channel's preferred probe model, see pickProbeBinding), records the verdict
// and notifies when the channel turns suspect.
func (p *channelProber) probe(ctx context.Context, channelID, modelID string) (verify.ProbeResult, error) {
	bindings := p.resolver.ChannelBindings(channelID)
	if len(bindings) == 0 {
		return verify.ProbeResult{}, fmt.Errorf("%w: channel %q has no enabled model binding", verify.ErrNoProbeTarget, channelID)
	}
	var recent []string
	if modelID == "" {
		recent = p.recentModels(ctx, channelID)
	}
	d, ok := pickProbeBinding(bindings, modelID, recent)
	if !ok {
		return verify.ProbeResult{}, fmt.Errorf("%w: channel %q does not serve model %q", verify.ErrNoProbeTarget, channelID, modelID)
	}
	wasSuspect, _ := p.verify.Suspect(channelID)
	res := p.verify.RecordProbe(p.prober.Probe(ctx, d))
	if suspect, why := p.verify.Suspect(channelID); suspect && !wasSuspect && p.notify != nil {
		name := d.Channel.Name
		if name == "" {
			name = channelID
		}
		p.notify(notify.Event{
			Kind:      notify.KindAuthenticitySuspect,
			Severity:  notify.SeverityWarning,
			Title:     "疑似掺水：" + name,
			Body:      why + "\n路由已优先使用其他渠道",
			ChannelID: channelID,
			Fields:    map[string]string{"model": res.ModelID, "score": strconv.Itoa(res.Score), "signals": strings.Join(res.Signals, ",")},
			At:        res.CheckedAt,
		})
	}
	return res, nil
}

// recentModels returns the logical models of the channel's latest requests,
// newest first.
func (p *channelProber) recentModels(ctx context.Context, channelID string) []string {
	if p.repo == nil {
		return nil
	}
	recs, err := p.repo.ListRequestRecordsByChannel(ctx, channelID, probeRecentRecords)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(recs))
	for _, rec := range recs {
		if rec.ModelID != "" {
			out = append(out, rec.ModelID)
		}
	}
	return out
}

// pass probes every enabled, routable and currently available channel once,
// one model each, sequentially.
// healthPass probes every channel whose breaker tripped and whose cooldown has
// elapsed: the channel is held out of routing until one cheap request proves it
// works (AUDIT §5 B5). A channel with no probeable binding is left untouched,
// so it is not parked half-open forever by a probe that can never run.
func (p *channelProber) healthPass(ctx context.Context) {
	if p.health == nil || p.resolver == nil || !p.health.RecoveryProbeEnabled() {
		return
	}
	if p.prober.Upstream == nil {
		return
	}
	now := time.Now()
	if p.now != nil {
		now = p.now()
	}
	for _, id := range p.health.ProbeCandidates(now) {
		if ctx.Err() != nil {
			return
		}
		bindings := p.resolver.ChannelBindings(id)
		d, ok := pickProbeBinding(bindings, "", nil)
		if !ok {
			continue
		}
		// Claim the probe before spending a request; a second pass (or a
		// concurrent one) must not double-probe.
		if !p.health.AllowProbe(id, now) {
			continue
		}
		res := p.prober.ProbeLiveness(ctx, d)
		if res.OK {
			p.health.RecordOutcome(id, true, res.Latency, now)
			p.log("relayhub: recovery probe: channel %s is back after %s", id, res.Latency.Round(time.Millisecond))
		} else {
			// A failed probe re-opens the breaker with a longer cooldown;
			// real traffic keeps flowing to the other channels meanwhile.
			p.health.RecordFailureReason(id, 0, now, "recovery probe: "+res.Error)
			p.log("relayhub: recovery probe: channel %s still down: %s", id, res.Error)
		}
		if p.spacing > 0 {
			t := time.NewTimer(p.spacing)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}
}

func (p *channelProber) pass(ctx context.Context) {
	if p.repo == nil {
		return
	}
	channels, err := p.repo.ListChannels(ctx, "")
	if err != nil {
		p.log("relayhub: authenticity probes: list channels: %v", err)
		return
	}
	for _, ch := range channels {
		if ctx.Err() != nil {
			return
		}
		if !ch.Enabled || !ch.RoutingEnabled {
			continue
		}
		// Open breakers, exhausted quota and dead credentials would only
		// burn requests; health brings those channels back on its own.
		if p.health != nil && !p.health.Available(ch.ID, time.Now()) {
			continue
		}
		res, err := p.probe(ctx, ch.ID, "")
		if err != nil {
			if !errors.Is(err, verify.ErrNoProbeTarget) {
				p.log("relayhub: authenticity probe of channel %s: %v", ch.ID, err)
			}
			continue
		}
		if res.Conclusive && res.Score < verify.SuspectBelow {
			p.log("relayhub: authenticity probe flagged channel %s (model %s, score %d): %s", ch.ID, res.ModelID, res.Score, strings.Join(res.Signals, ", "))
		}
		if p.spacing > 0 {
			t := time.NewTimer(p.spacing)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}
}

func (p *channelProber) log(format string, args ...any) {
	if p.logf != nil {
		p.logf(format, args...)
	}
}

// pickProbeBinding chooses what to probe on a channel. An explicit model
// (logical ID or upstream name) must be served; otherwise the channel's
// default test model, then its most used recent model (ties: most recent),
// then its first binding.
func pickProbeBinding(bindings []router.Decision, modelID string, recent []string) (router.Decision, bool) {
	if len(bindings) == 0 {
		return router.Decision{}, false
	}
	find := func(name string) (router.Decision, bool) {
		for _, d := range bindings {
			if d.Model.ID == name {
				return d, true
			}
		}
		for _, d := range bindings {
			if d.ProviderModel.UpstreamModelName != "" && d.ProviderModel.UpstreamModelName == name {
				return d, true
			}
		}
		return router.Decision{}, false
	}
	if modelID != "" {
		return find(modelID)
	}
	if def := strings.TrimSpace(bindings[0].Channel.DefaultTestModel); def != "" {
		if d, ok := find(def); ok {
			return d, true
		}
	}
	counts := map[string]int{}
	for _, id := range recent {
		counts[id]++
	}
	best, bestCount := "", 0
	for _, id := range recent { // newest first: a strict > keeps the most recent on ties
		if counts[id] > bestCount {
			if _, ok := find(id); ok {
				best, bestCount = id, counts[id]
			}
		}
	}
	if best != "" {
		return find(best)
	}
	return bindings[0], true
}

// runProbeLoop calls pass after initialDelay and then every interval until
// ctx ends. A non-positive interval disables it.
func runProbeLoop(ctx context.Context, interval, initialDelay time.Duration, pass func(context.Context)) {
	if interval <= 0 {
		return
	}
	t := time.NewTimer(initialDelay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pass(ctx)
		t.Reset(interval)
	}
}

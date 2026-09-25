package app

import (
	"context"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/gateway"
	"relayhub/internal/health"
	"relayhub/internal/notify"
	"relayhub/internal/router"
	"relayhub/internal/verify"
)

// probeRepo is the slice of the repository the prober reads.
type probeRepo interface {
	ListChannels(ctx context.Context, providerID string) ([]domain.Channel, error)
	ListRequestRecordsByChannel(ctx context.Context, channelID string, limit int) ([]domain.RequestRecord, error)
}

// channelProber runs authenticity probes. Not implemented yet.
type channelProber struct {
	resolver *router.Resolver
	prober   gateway.Prober
	verify   *verify.Registry
	repo     probeRepo
	health   *health.Registry
	notify   func(notify.Event)
	logf     func(string, ...any)
	spacing  time.Duration
}

func (p *channelProber) probe(ctx context.Context, channelID, modelID string) (verify.ProbeResult, error) {
	return verify.ProbeResult{}, nil
}

func (p *channelProber) pass(ctx context.Context) {}

func pickProbeBinding(bindings []router.Decision, modelID string, recent []string) (router.Decision, bool) {
	return router.Decision{}, false
}

func runProbeLoop(ctx context.Context, interval, initialDelay time.Duration, pass func(context.Context)) {
}

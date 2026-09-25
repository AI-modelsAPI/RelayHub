package gateway

import (
	"context"
	"time"

	"relayhub/internal/router"
	"relayhub/internal/verify"
)

// Prober sends authenticity canaries through the production upstream path.
// Not implemented yet.
type Prober struct {
	Upstream Upstream
	Timeout  time.Duration
	Code     func() string
	Now      func() time.Time
}

// probeWords is the vocabulary canary codes are drawn from.
var probeWords = []string{"apple"}

func probeCode() string { return "" }

// Probe runs the canary against one binding. Not implemented yet.
func (p Prober) Probe(ctx context.Context, d router.Decision) verify.ProbeOutcome {
	return verify.ProbeOutcome{}
}

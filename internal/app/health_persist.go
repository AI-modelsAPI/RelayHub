package app

import (
	"context"
	"fmt"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/health"
)

// healthRecordWriter is the persistence subset needed by the health observer.
type healthRecordWriter interface {
	CreateHealthRecord(context.Context, domain.HealthRecord) error
}

// healthPersister returns a health.Registry observer that writes one
// HealthRecord per status transition (healthy -> circuit_open, -> quota
// exhausted, -> healthy again, ...). Per-request outcomes are deliberately not
// persisted: they already land in request_records, and the breaker's
// exponential backoff bounds the transition rate of a dead upstream.
func healthPersister(repo healthRecordWriter, logf func(string, ...any)) func(health.Transition) {
	return func(tr health.Transition) {
		if repo == nil {
			return
		}
		rec := domain.HealthRecord{
			ID:        fmt.Sprintf("hr-%s-%d", tr.ChannelID, tr.At.UnixNano()),
			ChannelID: tr.ChannelID,
			Status:    string(tr.To),
			CheckedAt: tr.At.UTC(),
			LatencyMS: int(tr.State.Latency / time.Millisecond),
		}
		switch {
		case tr.State.LastError != "" && (tr.To == health.CircuitOpen || tr.To == health.Degraded || tr.To == health.Cooldown):
			rec.ErrorMessage = tr.State.LastError
		case tr.To == health.QuotaExhausted:
			rec.ErrorMessage = "quota exhausted"
		case tr.To == health.AuthExpired:
			rec.ErrorMessage = "authentication expired"
		}
		if tr.From != "" {
			rec.ErrorMessage = trimJoin(fmt.Sprintf("from %s", tr.From), rec.ErrorMessage)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := repo.CreateHealthRecord(ctx, rec); err != nil && logf != nil {
			logf("relayhub: persist health transition for %s: %v", tr.ChannelID, err)
		}
	}
}

func trimJoin(a, b string) string {
	if b == "" {
		return a
	}
	return a + ": " + b
}

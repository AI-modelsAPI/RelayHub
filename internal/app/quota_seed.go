package app

import (
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/health"
)

// quotaSeedMaxAge bounds how old a persisted balance snapshot may be to still
// influence routing after a restart. Older snapshots are treated as unknown.
const quotaSeedMaxAge = 48 * time.Hour

func quotaUpdateFrom(q domain.QuotaSnapshot) health.QuotaUpdate {
	u := health.QuotaUpdate{USD: q.AvailableUSD, Remaining: q.Remaining, Known: q.Known(), Source: q.Source}
	if q.ResetAt != nil {
		u.ResetAt = *q.ResetAt
	}
	return u
}

// seedQuotaFromChannels loads structured Channel.QuotaState snapshots into the
// health registry. Legacy free-form quota_state values are ignored.
func seedQuotaFromChannels(reg *health.Registry, channels map[string]domain.Channel, now time.Time) int {
	seeded := 0
	for id, ch := range channels {
		q, ok := domain.ParseQuotaSnapshot(ch.QuotaState)
		if !ok || now.Sub(q.UpdatedAt) > quotaSeedMaxAge {
			continue
		}
		reg.SetQuota(id, quotaUpdateFrom(q), q.UpdatedAt)
		seeded++
	}
	return seeded
}

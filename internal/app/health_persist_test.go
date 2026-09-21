package app

import (
	"context"
	"testing"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/health"
)

type memHealthRepo struct{ records []domain.HealthRecord }

func (m *memHealthRepo) CreateHealthRecord(_ context.Context, r domain.HealthRecord) error {
	m.records = append(m.records, r)
	return nil
}

// Health state used to live only in memory: a restart forgot every breaker
// trip. Transitions must now land in health_records, while steady-state
// traffic must not generate rows.
func TestHealthPersisterWritesTransitionsOnly(t *testing.T) {
	repo := &memHealthRepo{}
	reg := health.NewRegistry()
	reg.SetObserver(healthPersister(repo, nil))
	now := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)

	for i := 0; i < 20; i++ {
		reg.RecordOutcome("c", true, 25*time.Millisecond, now)
	}
	if len(repo.records) != 1 || repo.records[0].Status != string(health.Healthy) {
		t.Fatalf("expected exactly one healthy record for steady traffic, got %+v", repo.records)
	}
	for i := 0; i < 3; i++ {
		reg.RecordFailureReason("c", 3, now, "upstream status 503")
	}
	if len(repo.records) != 2 {
		t.Fatalf("expected breaker trip to be persisted once, got %+v", repo.records)
	}
	got := repo.records[1]
	if got.ChannelID != "c" || got.Status != string(health.CircuitOpen) || got.LatencyMS != 25 {
		t.Fatalf("record content: %+v", got)
	}
	if got.ErrorMessage != "from healthy: upstream status 503" {
		t.Fatalf("error message should carry cause and previous state: %q", got.ErrorMessage)
	}
	if !got.CheckedAt.Equal(now) {
		t.Fatalf("checked_at must be the transition time: %v", got.CheckedAt)
	}
}

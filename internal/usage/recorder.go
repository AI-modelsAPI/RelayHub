package usage

import (
	"context"
	"sync"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
)

// RequestRecord is the metadata-only usage record persisted by the gateway.
type RequestRecord = domain.RequestRecord

// Recorder stores request metadata. Implementations must not retain prompts,
// request bodies, response bodies, or credentials.
type Recorder interface {
	Record(context.Context, RequestRecord) error
}

// RepositoryRecorder adapts the existing metadata repository to Recorder.
type RepositoryRecorder struct{ Repo repository.StatusRepository }

func (r RepositoryRecorder) Record(ctx context.Context, record RequestRecord) error {
	if r.Repo == nil {
		return nil
	}
	return r.Repo.CreateRequestRecord(ctx, record)
}

// MemoryRecorder is useful for local tests and diagnostics. It stores only
// RequestRecord values and is safe for concurrent use.
type MemoryRecorder struct {
	mu      sync.Mutex
	records []RequestRecord
}

func (r *MemoryRecorder) Record(ctx context.Context, record RequestRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	r.records = append(r.records, record)
	r.mu.Unlock()
	return nil
}

func (r *MemoryRecorder) Records() []RequestRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RequestRecord, len(r.records))
	copy(out, r.records)
	return out
}

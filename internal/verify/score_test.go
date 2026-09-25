package verify

import (
	"testing"

	"relayhub/internal/domain"
)

func TestObserveDropsOnMismatch(t *testing.T) {
	r := New()
	s := r.Observe(domain.RequestRecord{ChannelID: "c1", StatusCode: 200, ModelID: "claude", UpstreamModel: "gpt-4o"})
	if s.Score >= 100 {
		t.Fatalf("expected penalty, got %+v", s)
	}
	if r.Get("c1").Score != s.Score {
		t.Fatal("registry mismatch")
	}
}

// A mid-stream upstream error event is a soft failure: the channel answered,
// but the answer was an error (AUDIT §5 B5).
func TestObservePenalisesMidStreamErrors(t *testing.T) {
	r := New()
	s := r.Observe(domain.RequestRecord{ChannelID: "c1", StatusCode: 200, ErrorClass: "upstream_error_event"})
	if s.Score >= 100 || !hasSignal(s.Signals, "upstream_error_event") {
		t.Fatalf("expected a penalty, got %+v", s)
	}
}

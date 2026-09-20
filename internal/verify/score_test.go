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

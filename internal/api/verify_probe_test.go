package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"relayhub/internal/affinity"
	"relayhub/internal/lab"
	"relayhub/internal/ratelimit"
	"relayhub/internal/verify"
)

// POST /api/v1/verify/probe used to answer with the passive score plus a
// "passive_only" marker. With a prober wired it must actually probe the
// channel and return the verdict (AUDIT §5 B1).
func TestVerifyProbeRunsTheWiredProber(t *testing.T) {
	s, err := NewConfiguredServer(Config{LocalOnly: true, Management: "127.0.0.1:8790"})
	if err != nil {
		t.Fatal(err)
	}
	reg := verify.New()
	s.WithControlPlane(reg, lab.NewRing(4), affinity.New(0), ratelimit.New())
	var gotChannel, gotModel string
	s.WithProber(func(_ context.Context, channelID, modelID string) (verify.ProbeResult, error) {
		gotChannel, gotModel = channelID, modelID
		return reg.RecordProbe(verify.ProbeOutcome{
			ChannelID: channelID, ModelID: "m", UpstreamModel: "gpt-4o", Protocol: "openai-chat",
			CanaryStatus: 200, CanaryText: "Hello! How can I help?",
		}), nil
	})
	h := s.Handler()

	w := request(t, h, http.MethodPost, "/api/v1/verify/probe", "", `{"channel_id":"c1","model_id":"m"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if gotChannel != "c1" || gotModel != "m" {
		t.Fatalf("prober called with %q/%q", gotChannel, gotModel)
	}
	var out struct {
		Result verify.ProbeResult `json:"result"`
		Score  verify.Score       `json:"score"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Result.Conclusive || out.Result.Score != 40 || len(out.Result.Checks) == 0 {
		t.Fatalf("result: %+v", out.Result)
	}
	if !out.Score.Suspect || out.Score.Score != 40 || out.Score.ChannelID != "c1" {
		t.Fatalf("score: %+v", out.Score)
	}
	for _, sig := range out.Score.Signals {
		if sig == "passive_only" {
			t.Fatalf("an active probe must not claim to be passive: %v", out.Score.Signals)
		}
	}

	// The scores endpoint reflects the verdict.
	w = request(t, h, http.MethodGet, "/api/v1/verify/scores", "", "")
	var scores struct {
		Scores []verify.Score `json:"scores"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &scores); err != nil || len(scores.Scores) != 1 || !scores.Scores[0].Suspect {
		t.Fatalf("scores: %v %s", err, w.Body.String())
	}

	w = request(t, h, http.MethodPost, "/api/v1/verify/probe", "", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing channel_id: status=%d body=%s", w.Code, w.Body.String())
	}

	s.WithProber(func(context.Context, string, string) (verify.ProbeResult, error) {
		return verify.ProbeResult{}, fmt.Errorf("%w: channel %q has no enabled model binding", verify.ErrNoProbeTarget, "ghost")
	})
	w = request(t, h, http.MethodPost, "/api/v1/verify/probe", "", `{"channel_id":"ghost"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown probe target: status=%d body=%s", w.Code, w.Body.String())
	}
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"relayhub/internal/billing"
	"relayhub/internal/repository"
)

// GET /api/v1/billing/reconcile is what the console's "today's spend" tile
// reads; POST runs a pass on demand (AUDIT §5 B2).
func TestBillingReconcileEndpoint(t *testing.T) {
	s, err := NewConfiguredServer(Config{LocalOnly: true, Management: "127.0.0.1:8790"})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	// Nothing wired: say so instead of pretending reconciliation happened.
	w := request(t, h, http.MethodGet, "/api/v1/billing/reconcile", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"supported":false`) {
		t.Fatalf("unwired read: status=%d body=%s", w.Code, w.Body.String())
	}

	reg := billing.NewRegistry()
	reg.Record(billing.Report{ChannelID: "c1", Verdict: billing.VerdictOK, Tokens: 1_000_000, EffectiveUSDPerMTok: 1, ChargedUSD: 1, DeclaredUSD: 2, SavingsUSD: 1})
	reg.Record(billing.Report{ChannelID: "c2", Verdict: billing.VerdictDrift, Tokens: 2_000_000, EffectiveUSDPerMTok: 3, ChargedUSD: 6})
	s.WithBilling(reg, nil)

	w = request(t, h, http.MethodGet, "/api/v1/billing/reconcile", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Supported bool             `json:"supported"`
		Reports   []billing.Report `json:"reports"`
		Summary   billing.Summary  `json:"summary"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Supported || len(out.Reports) != 2 || out.Reports[0].ChannelID != "c1" {
		t.Fatalf("reports: %+v", out)
	}
	if out.Summary.Channels != 2 || out.Summary.Drift != 1 || out.Summary.SavingsUSD != 1 || out.Summary.ChargedUSD != 7 {
		t.Fatalf("summary: %+v", out.Summary)
	}

	// Without a wired pass the POST must not claim to have reconciled.
	w = request(t, h, http.MethodPost, "/api/v1/billing/reconcile", "", `{"channel_id":"c1"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired POST: status=%d body=%s", w.Code, w.Body.String())
	}

	var gotChannel string
	s.WithBilling(reg, func(_ context.Context, channelID string) ([]billing.Report, error) {
		gotChannel = channelID
		if channelID == "ghost" {
			return nil, repository.ErrNotFound
		}
		return []billing.Report{{ChannelID: channelID, Verdict: billing.VerdictUnknown, Reason: "站点日志里没有本窗口的消费记录"}}, nil
	})
	w = request(t, h, http.MethodPost, "/api/v1/billing/reconcile", "", `{"channel_id":"c9"}`)
	if w.Code != http.StatusOK || gotChannel != "c9" {
		t.Fatalf("on-demand pass: status=%d channel=%q body=%s", w.Code, gotChannel, w.Body.String())
	}
	var run struct {
		Reports []billing.Report `json:"reports"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil || len(run.Reports) != 1 || run.Reports[0].ChannelID != "c9" {
		t.Fatalf("run response: %v %s", err, w.Body.String())
	}

	w = request(t, h, http.MethodPost, "/api/v1/billing/reconcile", "", `{"channel_id":"ghost"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown channel: status=%d body=%s", w.Code, w.Body.String())
	}

	w = request(t, h, http.MethodDelete, "/api/v1/billing/reconcile", "", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE: status=%d body=%s", w.Code, w.Body.String())
	}
}

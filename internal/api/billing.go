package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"relayhub/internal/billing"
	"relayhub/internal/repository"
)

// Billing reconciliation endpoints (AUDIT §5 B2).
//
//	GET  /api/v1/billing/reconcile -> newest report per channel + summary
//	POST /api/v1/billing/reconcile {"channel_id":"..."} -> run a pass now
//	     (channel_id empty = every enabled channel)
//
// The reports are metadata: token counts, money and the site's own billing
// ratios. They are what the console shows and what the "cheapest" routing
// strategy ranks channels by.

// billingReconcile serves both reads and on-demand passes.
func (s *Server) billingReconcile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.billingReports(w, r)
	case http.MethodPost:
		s.billingRun(w, r)
	default:
		s.methodAllowed(w, r, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) billingReports(w http.ResponseWriter, r *http.Request) {
	reports := []billing.Report{}
	if s.Billing != nil {
		reports = s.Billing.Reports()
	}
	s.write(w, r, http.StatusOK, map[string]any{
		"supported": s.Billing != nil,
		"reports":   reports,
		"summary":   billing.Summarize(reports),
	})
}

func (s *Server) billingRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ChannelID string `json:"channel_id"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
	in.ChannelID = strings.TrimSpace(in.ChannelID)
	if s.Reconciler == nil {
		s.fail(w, r, unavailable("billing reconciliation is not configured"))
		return
	}
	reports, err := s.Reconciler(r.Context(), in.ChannelID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			s.fail(w, r, notFound("channel not found"))
			return
		}
		s.fail(w, r, internal(err))
		return
	}
	if reports == nil {
		reports = []billing.Report{}
	}
	s.auditEvent(r.Context(), "billing_reconcile", r, map[string]string{"channel": in.ChannelID})
	s.write(w, r, http.StatusOK, map[string]any{
		"reports": reports,
		"summary": billing.Summarize(reports),
	})
}

// WithBilling wires the reconciliation registry and the on-demand pass.
func (s *Server) WithBilling(registry *billing.Registry, run func(context.Context, string) ([]billing.Report, error)) {
	s.Billing = registry
	s.Reconciler = run
}

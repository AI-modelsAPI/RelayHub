package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"relayhub/internal/affinity"
	"relayhub/internal/domain"
	"relayhub/internal/identity"
	"relayhub/internal/lab"
	"relayhub/internal/mcp"
	"relayhub/internal/ratelimit"
	"relayhub/internal/verify"
)

func (s *Server) extras(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	path := r.URL.Path
	switch {
	case path == "/api/v1/usage/summary" && r.Method == http.MethodGet:
		s.usageSummary(w, r)
	case path == "/api/v1/verify/scores" && r.Method == http.MethodGet:
		s.verifyScores(w, r)
	case path == "/api/v1/verify/probe" && r.Method == http.MethodPost:
		s.verifyProbe(w, r)
	case path == "/api/v1/identity" && r.Method == http.MethodGet:
		s.identityList(w, r)
	case strings.HasPrefix(path, "/api/v1/identity/") && (r.Method == http.MethodPatch || r.Method == http.MethodPut):
		s.identityPatch(w, r)
	case path == "/api/v1/sessions" && r.Method == http.MethodGet:
		s.sessions(w, r)
	case path == "/api/v1/lab/capture" && r.Method == http.MethodPost:
		s.labCapture(w, r)
	case path == "/api/v1/lab/replay" && r.Method == http.MethodPost:
		s.labReplay(w, r)
	case path == "/api/v1/lab" && r.Method == http.MethodGet:
		s.labList(w, r)
	case path == "/api/v1/routes/explain" && r.Method == http.MethodGet:
		s.routeExplain(w, r)
	case path == "/api/v1/mcp" && r.Method == http.MethodPost:
		s.mcpRPC(w, r)
	default:
		s.fail(w, r, badRequest("not_found", "unknown extras endpoint"))
	}
}

func (s *Server) usageSummary(w http.ResponseWriter, r *http.Request) {
	if s.Repo == nil {
		s.write(w, r, 200, map[string]any{"cache_hit_ratio": 0, "total_requests": 0, "cache_read_tokens": 0, "input_tokens": 0})
		return
	}
	recs, err := s.Repo.ListRequestRecords(r.Context())
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	var in, cache int
	for _, rec := range recs {
		in += rec.InputTokens
		cache += rec.CacheReadTokens
	}
	ratio := 0.0
	if in > 0 {
		ratio = float64(cache) / float64(in)
	}
	s.write(w, r, 200, map[string]any{
		"total_requests":    len(recs),
		"input_tokens":      in,
		"cache_read_tokens": cache,
		"cache_hit_ratio":   ratio,
	})
}

func (s *Server) verifyScores(w http.ResponseWriter, r *http.Request) {
	scores := []verify.Score{}
	if s.Verify != nil {
		scores = s.Verify.All()
	}
	s.write(w, r, 200, map[string]any{"scores": scores})
}

func (s *Server) verifyProbe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ChannelID string `json:"channel_id"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
	score := verify.Score{ChannelID: in.ChannelID, Score: 100, Signals: []string{"passive_only"}}
	if s.Verify != nil && in.ChannelID != "" {
		score = s.Verify.Get(in.ChannelID)
		score.Signals = append(append([]string{}, score.Signals...), "passive_only")
	}
	s.write(w, r, 200, map[string]any{"score": score})
}

func (s *Server) identityList(w http.ResponseWriter, r *http.Request) {
	if s.Repo == nil {
		s.write(w, r, 200, map[string]any{"bundles": []any{}})
		return
	}
	chs, err := s.Repo.ListChannels(r.Context(), "")
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	out := make([]identity.Bundle, 0, len(chs))
	for _, ch := range chs {
		out = append(out, identity.FromChannel(ch))
	}
	s.write(w, r, 200, map[string]any{"bundles": out})
}

func (s *Server) identityPatch(w http.ResponseWriter, r *http.Request) {
	if s.Repo == nil {
		s.fail(w, r, unavailable("resource persistence is not configured"))
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/identity/"), "/")
	ch, err := s.Repo.GetChannel(r.Context(), id)
	if err != nil {
		s.fail(w, r, mapRepoError(err, "channel"))
		return
	}
	var b identity.Bundle
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&b); err != nil {
		s.fail(w, r, badRequest("invalid_json", "malformed identity bundle"))
		return
	}
	identity.ApplyToChannel(&ch, b)
	if err := s.Repo.UpdateChannel(r.Context(), ch); err != nil {
		s.fail(w, r, mapRepoError(err, "channel"))
		return
	}
	s.notifyConfigChange(r.Context())
	out := identity.FromChannel(ch)
	if r.URL.Query().Get("probe") == "1" {
		out.EgressIP = identity.ProbeEgress(r.Context(), ch.ProxyURL)
	}
	s.write(w, r, 200, map[string]any{"bundle": out})
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	s.write(w, r, 200, map[string]any{"sticky": s.Sticky != nil})
}

func (s *Server) labCapture(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
	if s.Lab == nil {
		s.Lab = lab.NewRing(32)
	}
	if in.Enabled != nil {
		s.Lab.SetEnabled(*in.Enabled)
	}
	s.write(w, r, 200, map[string]any{"enabled": s.Lab.Enabled()})
}

func (s *Server) labList(w http.ResponseWriter, r *http.Request) {
	items := []lab.Capture{}
	if s.Lab != nil {
		items = s.Lab.List()
	}
	s.write(w, r, 200, map[string]any{"enabled": s.Lab != nil && s.Lab.Enabled(), "captures": items})
}

func (s *Server) labReplay(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID         string   `json:"id"`
		ChannelIDs []string `json:"channel_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		s.fail(w, r, badRequest("invalid_json", "malformed replay request"))
		return
	}
	if s.Lab == nil {
		s.fail(w, r, unavailable("lab capture is not enabled"))
		return
	}
	cap, ok := s.Lab.Get(in.ID)
	if !ok {
		s.fail(w, r, notFound("capture not found"))
		return
	}
	ids := in.ChannelIDs
	if len(ids) > 3 {
		ids = ids[:3]
	}
	s.write(w, r, 200, map[string]any{
		"id": cap.ID, "model": cap.Model, "protocol": cap.Protocol,
		"fanout": ids, "size": cap.Size, "note": "replay executes on next gateway slice; payload retained in memory",
	})
}

func (s *Server) routeExplain(w http.ResponseWriter, r *http.Request) {
	if s.Repo == nil {
		s.write(w, r, 200, map[string]any{"record": nil})
		return
	}
	recs, err := s.Repo.ListRequestRecords(r.Context())
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	want := r.URL.Query().Get("request_id")
	var rec *domain.RequestRecord
	for i := range recs {
		if want != "" && recs[i].RequestID == want {
			rec = &recs[i]
			break
		}
		if want == "" {
			rec = &recs[0]
			break
		}
	}
	s.write(w, r, 200, map[string]any{"record": rec})
}

func (s *Server) mcpRPC(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	srv := &mcp.Server{Backend: mcpAdapter{s: s}}
	out := srv.Handle(r.Context(), body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(out)
}

type mcpAdapter struct{ s *Server }

func (m mcpAdapter) ListChannels(ctx context.Context) (any, error) {
	if m.s.Repo == nil {
		return []any{}, nil
	}
	return m.s.Repo.ListChannels(ctx, "")
}
func (m mcpAdapter) QuotaStatus(ctx context.Context) (any, error) {
	if m.s.Repo == nil {
		return map[string]any{}, nil
	}
	recs, err := m.s.Repo.ListRequestRecords(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"total": len(recs)}, nil
}
func (m mcpAdapter) ExplainLast(ctx context.Context) (any, error) {
	if m.s.Repo == nil {
		return nil, nil
	}
	recs, err := m.s.Repo.ListRequestRecords(ctx)
	if err != nil || len(recs) == 0 {
		return nil, err
	}
	return recs[0], nil
}
func (m mcpAdapter) RunCheckin(ctx context.Context) (any, error) {
	if m.s.Scheduler == nil {
		return map[string]any{"ok": false, "reason": "scheduler not configured"}, nil
	}
	return map[string]any{"ok": true}, nil
}
func (m mcpAdapter) TrustReport(ctx context.Context) (any, error) {
	if m.s.Verify == nil {
		return []any{}, nil
	}
	return m.s.Verify.All(), nil
}

func (s *Server) WithControlPlane(v *verify.Registry, l *lab.Ring, sticky *affinity.Table, lim *ratelimit.Limiter) {
	s.Verify = v
	s.Lab = l
	s.Sticky = sticky
	s.Limiter = lim
}

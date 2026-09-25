package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"relayhub/internal/affinity"
	"relayhub/internal/domain"
	"relayhub/internal/identity"
	"relayhub/internal/lab"
	"relayhub/internal/mcp"
	"relayhub/internal/ratelimit"
	"relayhub/internal/secretfields"
	"relayhub/internal/verify"
)

func (s *Server) usageSummary(w http.ResponseWriter, r *http.Request) {
	if !s.methodAllowed(w, r, http.MethodGet) {
		return
	}
	if s.Repo == nil {
		s.write(w, r, 200, map[string]any{"cache_hit_ratio": 0, "total_requests": 0, "cache_read_tokens": 0, "input_tokens": 0})
		return
	}
	// Aggregates are computed in SQL; loading the full history into memory was
	// a capacity-governance gap (AUDIT RH-32).
	total, in, cache, err := s.Repo.RequestUsageTotals(r.Context())
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	ratio := 0.0
	if in > 0 {
		ratio = float64(cache) / float64(in)
	}
	s.write(w, r, 200, map[string]any{
		"total_requests":    total,
		"input_tokens":      in,
		"cache_read_tokens": cache,
		"cache_hit_ratio":   ratio,
	})
}

func (s *Server) verifyScores(w http.ResponseWriter, r *http.Request) {
	if !s.methodAllowed(w, r, http.MethodGet) {
		return
	}
	scores := []verify.Score{}
	if s.Verify != nil {
		scores = s.Verify.All()
	}
	s.write(w, r, 200, map[string]any{"scores": scores})
}

// verifyProbe runs an active authenticity probe (AUDIT §5 B1) against a
// channel: a canary and, for tool-capable models, a forced tool call, sent
// through the channel's credential, egress and identity headers. model_id is
// optional (default: the channel's default test model, then its most used
// model). Without a wired prober it reports the passive score.
func (s *Server) verifyProbe(w http.ResponseWriter, r *http.Request) {
	if !s.methodAllowed(w, r, http.MethodPost) {
		return
	}
	var in struct {
		ChannelID string `json:"channel_id"`
		ModelID   string `json:"model_id"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
	in.ChannelID, in.ModelID = strings.TrimSpace(in.ChannelID), strings.TrimSpace(in.ModelID)
	if s.Prober == nil {
		score := verify.Score{ChannelID: in.ChannelID, Score: 100, Passive: 100, Signals: []string{"passive_only"}}
		if s.Verify != nil && in.ChannelID != "" {
			score = s.Verify.Get(in.ChannelID)
			score.Signals = append(append([]string{}, score.Signals...), "passive_only")
		}
		s.write(w, r, 200, map[string]any{"score": score})
		return
	}
	if in.ChannelID == "" {
		s.fail(w, r, badRequest("validation_error", "channel_id is required"))
		return
	}
	res, err := s.Prober(r.Context(), in.ChannelID, in.ModelID)
	if err != nil {
		if errors.Is(err, verify.ErrNoProbeTarget) {
			s.fail(w, r, notFound(err.Error()))
			return
		}
		s.fail(w, r, internal(err))
		return
	}
	s.auditEvent(r.Context(), "verify_probe", r, map[string]string{"channel": in.ChannelID, "model": res.ModelID})
	score := verify.Score{ChannelID: in.ChannelID, Score: 100, Passive: 100}
	if s.Verify != nil {
		score = s.Verify.Get(in.ChannelID)
	}
	s.write(w, r, 200, map[string]any{"result": res, "score": score})
}

func (s *Server) identityList(w http.ResponseWriter, r *http.Request) {
	if !s.methodAllowed(w, r, http.MethodGet) {
		return
	}
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
		b := identity.FromChannel(ch)
		// Proxy credentials must never be echoed by the management API
		// (AUDIT RH-31).
		b.ProxyURL = maskProxyUserinfo(b.ProxyURL)
		out = append(out, b)
	}
	s.write(w, r, 200, map[string]any{"bundles": out})
}

func (s *Server) identityPatch(w http.ResponseWriter, r *http.Request) {
	if !s.methodAllowed(w, r, http.MethodPatch, http.MethodPut) {
		return
	}
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
	// PATCH semantics: an absent field keeps the stored value, an explicit
	// empty string clears it. The old handler decoded into a plain Bundle, so
	// a request that only set user_agent carried proxy_url="" and silently
	// switched the channel to a direct connection — exposing the operator's
	// real IP to the relay (AUDIT 2026-09-24 F12). It also skipped channel
	// validation and the audit log.
	var in struct {
		ChannelID string  `json:"channel_id"`
		ProxyURL  *string `json:"proxy_url"`
		UserAgent *string `json:"user_agent"`
		Timezone  *string `json:"timezone"`
		// Read-only fields a client may echo back from GET; ignored.
		EgressIP *string `json:"egress_ip"`
		Drift    *bool   `json:"drift"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		s.fail(w, r, badRequest("invalid_json", "malformed identity bundle"))
		return
	}
	if in.ChannelID != "" && in.ChannelID != ch.ID {
		s.fail(w, r, badRequest("validation_error", "channel_id does not match the request path"))
		return
	}
	changed := []string{}
	if in.ProxyURL != nil {
		next := strings.TrimSpace(*in.ProxyURL)
		// A client that GETs the (masked) bundle and PATCHes it back must not
		// overwrite the stored proxy password with the masked form.
		if next != ch.ProxyURL && !(next != "" && next == maskProxyUserinfo(ch.ProxyURL)) {
			ch.ProxyURL = next
			changed = append(changed, "proxy_url")
		}
	}
	if ch.CustomHeaders == nil {
		ch.CustomHeaders = map[string]string{}
	}
	setHeader := func(field, header string, v *string) {
		if v == nil {
			return
		}
		next := strings.TrimSpace(*v)
		if next == ch.CustomHeaders[header] {
			return
		}
		if next == "" {
			delete(ch.CustomHeaders, header)
		} else {
			ch.CustomHeaders[header] = next
		}
		changed = append(changed, field)
	}
	setHeader("user_agent", "User-Agent", in.UserAgent)
	setHeader("timezone", "X-Timezone", in.Timezone)
	if err := validateChannel(ch); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(changed) > 0 {
		if err := s.Repo.UpdateChannel(r.Context(), ch); err != nil {
			s.fail(w, r, mapRepoError(err, "channel"))
			return
		}
		s.auditEvent(r.Context(), "identity_update", r, map[string]string{"channel_id": ch.ID, "fields": strings.Join(changed, ",")})
		s.notifyConfigChange(r.Context())
	}
	out := identity.FromChannel(ch)
	if r.URL.Query().Get("probe") == "1" {
		// Probe through the exact egress the channel's traffic uses (global
		// default + fail-closed channel proxy), not a lookalike client
		// (AUDIT 2026-09-24 F13).
		out.EgressIP = identity.ProbeEgressWith(r.Context(), s.upstreamProbeClient(ch, 8*time.Second))
	}
	// Proxy credentials are never echoed (AUDIT RH-31); the PATCH response
	// used to return them unmasked.
	out.ProxyURL = maskProxyUserinfo(out.ProxyURL)
	s.write(w, r, 200, map[string]any{"bundle": out})
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	if !s.methodAllowed(w, r, http.MethodGet) {
		return
	}
	s.write(w, r, 200, map[string]any{"sticky": s.Sticky != nil})
}

func (s *Server) labCapture(w http.ResponseWriter, r *http.Request) {
	if !s.methodAllowed(w, r, http.MethodPost, http.MethodDelete) {
		return
	}
	if r.Method == http.MethodDelete {
		if s.Lab != nil {
			s.Lab.Clear()
		}
		s.write(w, r, 200, map[string]any{"cleared": true})
		return
	}
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
	if !s.methodAllowed(w, r, http.MethodGet) {
		return
	}
	items := []lab.Capture{}
	if s.Lab != nil {
		items = s.Lab.List()
	}
	s.write(w, r, 200, map[string]any{"enabled": s.Lab != nil && s.Lab.Enabled(), "captures": items})
}

func (s *Server) labReplay(w http.ResponseWriter, r *http.Request) {
	if !s.methodAllowed(w, r, http.MethodPost) {
		return
	}
	// Honest capability signalling (AUDIT RH-25): replaying a captured payload
	// through the gateway is not implemented; the previous response claimed a
	// replay would "execute on the next gateway slice", which never happened.
	s.fail(w, r, unsupported("lab replay is not implemented yet"))
}

func (s *Server) routeExplain(w http.ResponseWriter, r *http.Request) {
	if !s.methodAllowed(w, r, http.MethodGet) {
		return
	}
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
	if !s.methodAllowed(w, r, http.MethodPost) {
		return
	}
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
	chs, err := m.s.Repo.ListChannels(ctx, "")
	if err != nil {
		return nil, err
	}
	// MCP output reaches LLM agents and their logs: same masking as the
	// management API (AUDIT 2026-09-24 F10; RH-31 covered only /channels).
	return safeChannels(chs), nil
}
func (m mcpAdapter) QuotaStatus(ctx context.Context) (any, error) {
	if m.s.Repo == nil {
		return map[string]any{}, nil
	}
	total, inputTokens, cacheRead, err := m.s.Repo.RequestUsageTotals(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"total": total, "input_tokens": inputTokens, "cache_read_tokens": cacheRead}, nil
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
	if m.s.Repo == nil {
		return map[string]any{"ok": false, "reason": "repository not configured"}, nil
	}
	// run_checkin previously returned ok without executing anything (AUDIT
	// RH-25). Trigger the real scheduler for every check-in enabled channel.
	chs, err := m.s.Repo.ListChannels(ctx, "")
	if err != nil {
		return nil, err
	}
	started := []string{}
	skipped := []string{}
	for _, ch := range chs {
		if !ch.CheckinEnabled {
			continue
		}
		if err := m.s.Scheduler.RunNow(ctx, ch.ID); err != nil {
			skipped = append(skipped, ch.ID+": "+err.Error())
		} else {
			started = append(started, ch.ID)
		}
	}
	return map[string]any{
		"ok":      true,
		"started": started,
		"skipped": skipped,
		"note":    "check-in runs asynchronously; consult /api/v1/checkin for results",
	}, nil
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

// WithProber wires the active authenticity probe behind POST
// /api/v1/verify/probe.
func (s *Server) WithProber(p func(ctx context.Context, channelID, modelID string) (verify.ProbeResult, error)) {
	s.Prober = p
}

// methodAllowed enforces the method contract on directly-registered extras
// endpoints (P0-1: previously the removed dispatcher performed these checks).
func (s *Server) methodAllowed(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, m := range methods {
		if r.Method == m {
			return true
		}
	}
	s.fail(w, r, methodNotAllowed())
	return false
}

// maskProxyUserinfo strips credentials from a proxy URL before it is echoed
// by the management API (AUDIT RH-31: proxy passwords were returned in clear).
func maskProxyUserinfo(raw string) string { return secretfields.MaskProxyURL(raw) }

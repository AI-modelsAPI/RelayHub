package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"relayhub/internal/auth"
	"relayhub/internal/domain"
	"relayhub/internal/egress"
	"relayhub/internal/guard"
	"relayhub/internal/health"
	"relayhub/internal/lab"
	"relayhub/internal/ratelimit"
	"relayhub/internal/router"
	"relayhub/internal/usage"
	"relayhub/internal/verify"
)

var (
	ErrUnsupportedPath = errors.New("unsupported gateway path")
	ErrInvalidRequest  = errors.New("invalid gateway request")
)

// RouteResolver is the routing subset required by Handler.
type RouteResolver interface {
	Resolve(context.Context, router.Request) (router.Decision, error)
}

type modelsResolver interface {
	RoutableModels() []domain.Model
}

type excludingResolver interface {
	ResolveExcluding(context.Context, router.Request, map[string]bool) (router.Decision, error)
}

// Upstream performs one immutable, protocol-specific request.
type Upstream interface {
	Do(context.Context, Request) (Response, error)
}

// Request and Response are protocol-neutral upstream envelopes. Body is owned
// by the caller and must be closed by an Upstream implementation before return
// on error, or by the gateway after a successful response.
type Request struct {
	Protocol string
	Path     string
	Headers  http.Header
	Body     []byte
	Stream   bool
	Decision router.Decision
}
type Response struct {
	StatusCode      int
	Header          http.Header
	Body            io.ReadCloser
	CredentialKeyID string
	RetryAfter      time.Duration
}

// HTTPUpstream is the production HTTP implementation used for compatible
// OpenAI and Anthropic endpoints.
type CredentialPick struct {
	Token string
	KeyID string
}

type HTTPUpstream struct {
	Client         *http.Client
	Credential     func(context.Context, router.Decision) (string, error)
	PickCredential func(context.Context, router.Decision) (CredentialPick, error)
	// ClientFor, when set, picks the client per channel so that requests
	// exit through the channel's egress proxy (Channel.ProxyURL > global
	// default > environment). It takes precedence over Client; returning nil
	// falls back to Client. Without it, a channel proxy is still honoured via
	// an ad-hoc transport.
	ClientFor    func(domain.Channel) *http.Client
	MaxBodyBytes int64
}

// fallbackEgress backs HTTPUpstream when no ClientFor is wired.
var fallbackEgress = egress.New("")

type boundedReadCloser struct {
	io.Reader
	io.Closer
}

func (u HTTPUpstream) Do(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	base := strings.TrimRight(req.Decision.Channel.BaseURL, "/")
	if base == "" {
		return Response{}, errors.New("upstream base URL is empty")
	}
	path := req.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	// Avoid a doubled version segment: the client endpoint path already carries
	// the API version (e.g. /v1/chat/completions), and users commonly configure
	// a base_url that also ends in /v1 (the UI even auto-appends it). Joining both
	// yields https://host/v1/v1/chat/completions -> upstream 404. Drop the leading
	// version segment from the path when the base already ends with the same one.
	for _, ver := range []string{"/v1", "/v1beta", "/v2"} {
		if strings.HasSuffix(base, ver) && strings.HasPrefix(path, ver+"/") {
			path = strings.TrimPrefix(path, ver)
			break
		}
	}
	endpoint, err := url.JoinPath(base, path)
	if err != nil {
		return Response{}, fmt.Errorf("build upstream URL: %w", err)
	}
	body := io.NopCloser(bytes.NewReader(req.Body))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return Response{}, err
	}
	if req.Headers != nil {
		httpReq.Header = req.Headers.Clone()
	} else {
		httpReq.Header = make(http.Header)
	}
	var pick CredentialPick
	if u.PickCredential != nil {
		var err error
		pick, err = u.PickCredential(ctx, req.Decision)
		if err != nil {
			return Response{}, err
		}
	} else if u.Credential != nil {
		credential, err := u.Credential(ctx, req.Decision)
		if err != nil {
			return Response{}, err
		}
		pick.Token = credential
	}
	if pick.Token != "" {
		if req.Protocol == "anthropic" || req.Protocol == "anthropic-messages" {
			httpReq.Header.Set("x-api-key", pick.Token)
		} else {
			httpReq.Header.Set("Authorization", "Bearer "+pick.Token)
		}
	}
	client := u.Client
	switch {
	case u.ClientFor != nil:
		if c := u.ClientFor(req.Decision.Channel); c != nil {
			client = c
		}
	case strings.TrimSpace(req.Decision.Channel.ProxyURL) != "":
		// No selector wired (tests, embedded use): still honour the channel
		// proxy, through the shared cached transports.
		client = fallbackEgress.StreamingClientFor(req.Decision.Channel)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	if u.MaxBodyBytes > 0 {
		resp.Body = boundedReadCloser{Reader: io.LimitReader(resp.Body, u.MaxBodyBytes), Closer: resp.Body}
	}
	return Response{
		StatusCode:      resp.StatusCode,
		Header:          resp.Header,
		Body:            resp.Body,
		CredentialKeyID: pick.KeyID,
		RetryAfter:      retryAfterHeader(resp.Header),
	}, nil
}

// Config controls the local gateway listener handler.
type Config struct {
	Resolver       RouteResolver
	Upstream       Upstream
	Auth           *auth.LocalKeyService
	Recorder       usage.Recorder
	Health         *health.Registry
	DisableKey     func(context.Context, string)
	MaxAttempts    int
	RequestTimeout time.Duration
	Now            func() time.Time
	Limiter        *ratelimit.Limiter
	Lab            *lab.Ring
	Guard          *guard.Guard
	Verify         *verify.Registry
}

type Handler struct {
	cfg Config
	seq atomic.Uint64
}

func New(cfg Config) *Handler {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 2
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Handler{cfg: cfg}
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.Header.Get("X-Request-ID")) == "" {
		r = r.Clone(r.Context())
		r.Header.Set("X-Request-ID", fmt.Sprintf("gw-%d", h.seq.Add(1)))
	}
	if h.cfg.Auth != nil && !h.cfg.Auth.Validate(extractKey(r)) {
		writeGatewayError(w, r, http.StatusUnauthorized, "authentication_error", "local API key is invalid")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		data := []any{}
		if mr, ok := h.cfg.Resolver.(modelsResolver); ok {
			for _, m := range mr.RoutableModels() {
				created := m.CreatedAt.Unix()
				if created <= 0 {
					created = 1710000000
				}
				data = append(data, map[string]any{
					"id":       m.ID,
					"object":   "model",
					"created":  created,
					"owned_by": "relayhub",
				})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
		return
	}
	if r.Method != http.MethodPost {
		writeGatewayError(w, r, http.StatusMethodNotAllowed, "client_error", "POST is required")
		return
	}
	protocol, endpoint, err := endpointProtocol(r.URL.Path)
	if err != nil {
		writeGatewayError(w, r, http.StatusNotFound, "client_error", err.Error())
		return
	}
	if h.cfg.Resolver == nil || h.cfg.Upstream == nil {
		writeGatewayError(w, r, http.StatusServiceUnavailable, "configuration_error", "gateway is not configured")
		return
	}
	if h.cfg.RequestTimeout > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), h.cfg.RequestTimeout)
		r = r.WithContext(ctx)
		defer cancel()
	}
	input, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeGatewayError(w, r, http.StatusBadRequest, "client_error", "could not read request")
		return
	}
	model, err := requestModel(protocol, input)
	if err != nil {
		writeGatewayError(w, r, http.StatusBadRequest, "client_error", err.Error())
		return
	}
	if h.cfg.Lab != nil && h.cfg.Lab.Enabled() {
		h.cfg.Lab.Push(lab.Capture{Model: model, Protocol: protocol, Body: append([]byte(nil), input...)})
	}
	tools, vision, reasoning := requestCapabilities(protocol, input)
	routeReq := router.Request{
		Protocol: protocol, Model: model,
		ToolCallRequired: tools, VisionRequired: vision, ReasoningRequired: reasoning,
		SessionKey: requestSessionKey(r.Header.Get("X-Session-Id"), input),
	}
	decision, err := h.cfg.Resolver.Resolve(r.Context(), routeReq)
	if err != nil {
		writeGatewayError(w, r, http.StatusBadRequest, classifyResolve(err), err.Error())
		return
	}
	if rr, ok := h.cfg.Resolver.(*router.Resolver); ok && rr.Sticky != nil && routeReq.SessionKey != "" {
		if _, kid, ok := rr.Sticky.Lookup(routeReq.SessionKey); ok {
			decision.PreferredKeyID = kid
		}
	}
	upstreamInput, err := transformRequest(protocol, endpoint, input, decision)
	if err != nil {
		writeGatewayError(w, r, http.StatusBadRequest, "client_error", err.Error())
		return
	}
	stream := requestStream(protocol, input)
	attempted := map[string]bool{decision.Channel.ID: true}
	var last Response
	var lastErr error
	// startReq is taken before the first upstream call so that the recorded
	// latency covers the upstream, not just the local write-back.
	startReq := time.Now()
	// switchChannel moves to the next candidate (pre-first-byte failover).
	// It returns false when no other candidate exists.
	switchChannel := func() bool {
		resolver, ok := h.cfg.Resolver.(excludingResolver)
		if !ok {
			return false
		}
		next, resolveErr := resolver.ResolveExcluding(r.Context(), routeReq, attempted)
		if resolveErr != nil {
			return false
		}
		decision = next
		attempted[decision.Channel.ID] = true
		upstreamInput, err = transformRequest(protocol, endpoint, input, decision)
		return err == nil
	}
	for attempt := 0; attempt < h.cfg.MaxAttempts; attempt++ {
		if err := r.Context().Err(); err != nil {
			writeGatewayError(w, r, http.StatusRequestTimeout, "timeout", "request canceled")
			return
		}
		upProto, upPath := upstreamEndpointAndProtocol(protocol, endpoint, decision)
		// Local AIMD pacing: short waits are absorbed here, longer ones
		// switch channel before spending an attempt.
		limKey := last.CredentialKeyID
		if limKey == "" {
			limKey = decision.Channel.ID
		}
		if wait := h.cfg.Limiter.Wait(limKey); wait > 0 && wait <= 2*time.Second {
			timer := time.NewTimer(wait)
			select {
			case <-r.Context().Done():
				timer.Stop()
				writeGatewayError(w, r, http.StatusRequestTimeout, "timeout", "request canceled")
				return
			case <-timer.C:
			}
		} else if wait > 2*time.Second {
			if switchChannel() {
				continue
			}
			if err != nil {
				break
			}
		}
		attemptStart := time.Now()
		last, lastErr = h.cfg.Upstream.Do(r.Context(), Request{Protocol: upProto, Path: upPath, Headers: requestHeaders(r, upProto, stream, decision), Body: upstreamInput, Stream: stream, Decision: decision})
		ttfb := time.Since(attemptStart)
		if lastErr == nil {
			obsKey := last.CredentialKeyID
			if obsKey == "" {
				obsKey = decision.Channel.ID
			}
			h.cfg.Limiter.Observe(obsKey, last.StatusCode, last.RetryAfter, last.Header)
		}
		// Every attempt outcome is fed to the health registry exactly once,
		// including the final one: a channel whose last try failed used to
		// stay "healthy" because only retried failures were recorded.
		retry, sameChannel := false, false
		switch {
		case lastErr != nil:
			if retryableNetwork(lastErr) {
				h.observeFailure(decision, "network: "+lastErr.Error(), nil)
				retry = attempt+1 < h.cfg.MaxAttempts
			}
		case last.StatusCode == http.StatusUnauthorized:
			if h.cfg.DisableKey != nil && last.CredentialKeyID != "" {
				h.cfg.DisableKey(r.Context(), last.CredentialKeyID)
			}
			h.observeFailure(decision, "upstream status 401", last.Header)
		case retryableStatus(last.StatusCode):
			h.observeFailure(decision, fmt.Sprintf("upstream status %d", last.StatusCode), last.Header)
			retry = attempt+1 < h.cfg.MaxAttempts
			// A short Retry-After is cheaper to honour than to fail over.
			sameChannel = last.StatusCode == http.StatusTooManyRequests && last.RetryAfter > 0 && last.RetryAfter <= 2*time.Second
		case last.StatusCode >= 200 && last.StatusCode < 300:
			// A successful upstream response closes the circuit breaker and
			// clears the failure count. Without this, a channel that tripped
			// the breaker (or is half-open on a probe) would never return to
			// Healthy even when working.
			if h.cfg.Health != nil {
				h.cfg.Health.RecordOutcome(decision.Channel.ID, true, ttfb, h.cfg.Now())
			}
		}
		if !retry {
			break
		}
		if last.Body != nil {
			_ = last.Body.Close()
			last.Body = nil
		}
		if sameChannel {
			timer := time.NewTimer(last.RetryAfter)
			select {
			case <-r.Context().Done():
				timer.Stop()
				writeGatewayError(w, r, http.StatusRequestTimeout, "timeout", "request canceled")
				return
			case <-timer.C:
			}
			continue
		}
		if !switchChannel() {
			if err != nil {
				break
			}
			// No alternative candidate: retry the same channel.
			continue
		}
	}
	if lastErr != nil {
		h.record(r, protocol, decision, nil, http.StatusBadGateway, time.Since(startReq).Milliseconds())
		writeGatewayError(w, r, http.StatusBadGateway, "network_error", "upstream request failed")
		return
	}
	defer func() {
		if last.Body != nil {
			_ = last.Body.Close()
		}
	}()
	if last.StatusCode < 200 || last.StatusCode >= 300 {
		h.record(r, protocol, decision, nil, last.StatusCode, time.Since(startReq).Milliseconds())
		writeUpstreamError(w, r, last)
		return
	}
	if rr, ok := h.cfg.Resolver.(*router.Resolver); ok && rr.Sticky != nil && routeReq.SessionKey != "" {
		rr.Sticky.Remember(routeReq.SessionKey, decision.Channel.ID, last.CredentialKeyID)
	}
	if stream {
		upProto := normalizeProtocol(decision.ProviderModel.Protocol)
		meta, err := writeSSE(w, last.Body, protocol, upProto, decision.Model.ID)
		if err != nil && !errors.Is(err, context.Canceled) {
			return
		}
		rec := recordFor(r, protocol, decision, nil, http.StatusOK, h.cfg.Now())
		rec.LatencyMS = int(time.Since(startReq).Milliseconds())
		rec.TTFTMS = meta.TTFTMS
		rec.InputTokens = meta.InputTokens
		rec.OutputTokens = meta.OutputTokens
		rec.CacheReadTokens = meta.CacheReadTokens
		rec.CacheWriteTokens = meta.CacheWriteTokens
		rec.FinishReason = meta.FinishReason
		if meta.UpstreamModel != "" {
			rec.UpstreamModel = meta.UpstreamModel
		}
		if h.cfg.Verify != nil {
			h.cfg.Verify.Observe(rec)
		}
		if h.cfg.Recorder != nil {
			_ = h.cfg.Recorder.Record(r.Context(), rec)
		}
		return
	}
	body, err := io.ReadAll(io.LimitReader(last.Body, 16<<20))
	if err != nil {
		writeGatewayError(w, r, http.StatusBadGateway, "provider_protocol", "malformed upstream response")
		return
	}
	transformed, err := transformResponse(protocol, body, decision, h.cfg.Now())
	if err != nil {
		writeGatewayError(w, r, http.StatusBadGateway, "provider_protocol", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(transformed)
	h.record(r, protocol, decision, transformed, http.StatusOK, time.Since(startReq).Milliseconds())
}

// maxRetryAfter caps how long an upstream Retry-After hint may park a channel;
// a misbehaving upstream must not be able to disable a channel for days.
const maxRetryAfter = 10 * time.Minute

// observeFailure feeds one failed attempt into the health registry and applies
// an upstream Retry-After hint when present.
func (h *Handler) observeFailure(decision router.Decision, reason string, hdr http.Header) {
	if h.cfg.Health == nil {
		return
	}
	now := h.cfg.Now()
	h.cfg.Health.RecordFailureReason(decision.Channel.ID, 0, now, reason)
	if hdr != nil {
		if d := parseRetryAfter(hdr.Get("Retry-After"), now); d > 0 {
			h.cfg.Health.ApplyRetryAfter(decision.Channel.ID, d, now)
		}
	}
}

// parseRetryAfter understands both delta-seconds and HTTP-date forms. Values in
// the past or unparsable return 0; large values are capped to maxRetryAfter.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		d = time.Duration(secs) * time.Second
	} else if at, err := http.ParseTime(v); err == nil {
		d = at.Sub(now)
	} else {
		return 0
	}
	if d <= 0 {
		return 0
	}
	if d > maxRetryAfter {
		d = maxRetryAfter
	}
	return d
}

func (h *Handler) record(r *http.Request, protocol string, decision router.Decision, body []byte, status int, latencyMS int64) {
	rec := recordFor(r, protocol, decision, body, status, h.cfg.Now())
	rec.LatencyMS = int(latencyMS)
	if h.cfg.Verify != nil {
		h.cfg.Verify.Observe(rec)
	}
	if h.cfg.Recorder != nil {
		_ = h.cfg.Recorder.Record(r.Context(), rec)
	}
}

func extractKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("x-api-key")); key != "" {
		return key
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	return strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
}
func endpointProtocol(path string) (string, string, error) {
	switch path {
	case "/v1/chat/completions":
		return "openai-chat", path, nil
	case "/v1/responses":
		return "openai-responses", path, nil
	case "/v1/embeddings":
		return "openai-chat", path, nil
	case "/v1/messages", "/messages":
		return "anthropic-messages", "/v1/messages", nil
	default:
		return "", "", ErrUnsupportedPath
	}
}
func requestHeaders(r *http.Request, protocol string, stream bool, decision router.Decision) http.Header {
	h := r.Header.Clone()
	h.Del("Authorization")
	h.Del("x-api-key")
	h.Del("Cookie")
	h.Del("Set-Cookie")
	h.Del("Proxy-Authorization")
	if protocol == "anthropic" || protocol == "anthropic-messages" {
		if h.Get("anthropic-version") == "" {
			h.Set("anthropic-version", "2023-06-01")
		}
		if h.Get("Content-Type") == "" {
			h.Set("Content-Type", "application/json")
		}
	}
	if stream {
		h.Set("Accept", "text/event-stream")
	}
	// Inject channel-specific custom headers (e.g. User-Agent, custom authorization headers)
	if decision.Channel.CustomHeaders != nil {
		for k, v := range decision.Channel.CustomHeaders {
			h.Set(k, v)
		}
	}
	return h
}
func retryableStatus(status int) bool {
	return status == 401 || status == 429 || status == 500 || status == 502 || status == 503 || status == 504
}
func retryableNetwork(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// retryAfterHeader reads Retry-After from a response header (0 when absent).
func retryAfterHeader(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	return parseRetryAfter(h.Get("Retry-After"), time.Now())
}
func classifyResolve(err error) string {
	if errors.Is(err, router.ErrModelNotFound) {
		return "client_error"
	}
	if errors.Is(err, router.ErrNoCandidates) {
		return "provider_unavailable"
	}
	return "configuration_error"
}

func upstreamEndpointAndProtocol(clientProtocol, clientEndpoint string, decision router.Decision) (string, string) {
	upProto := normalizeProtocol(decision.ProviderModel.Protocol)
	if upProto == "" {
		return clientProtocol, clientEndpoint
	}
	clientNorm := normalizeProtocol(clientProtocol)
	if clientNorm == upProto {
		return clientProtocol, clientEndpoint
	}
	switch upProto {
	case "openai-chat":
		return "openai-chat", "/v1/chat/completions"
	case "anthropic-messages":
		return "anthropic-messages", "/v1/messages"
	default:
		return clientProtocol, clientEndpoint
	}
}

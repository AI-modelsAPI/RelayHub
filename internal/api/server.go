package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"relayhub/internal/affinity"
	"relayhub/internal/audit"
	"relayhub/internal/auth"
	"relayhub/internal/browser"
	"relayhub/internal/checkin"
	"relayhub/internal/clisync"
	"relayhub/internal/clisync/claude"
	"relayhub/internal/clisync/codex"
	"relayhub/internal/clisync/hermes"
	"relayhub/internal/domain"
	"relayhub/internal/export"
	"relayhub/internal/lab"
	"relayhub/internal/logging"
	"relayhub/internal/ratelimit"
	"relayhub/internal/verify"
	"relayhub/internal/repository"
	"relayhub/internal/secrets"
	webassets "relayhub/internal/web"
)

const apiPrefix = "/api/v1/"

// Server is the management HTTP handler. Local servers are safe to mount
// directly; remote servers can only be served through NewServerOnListener,
// which enforces the listener peer policy before routing requests.
type Server struct {
	Token          string
	Repo           repository.ResourceRepository
	LocalOnly      bool
	Management     string
	SecretStore    *secrets.Store
	Logger         *logging.Logger
	AuditLogger    *audit.Logger
	LocalKeys      *auth.LocalKeyService
	OnConfigChange func(context.Context) error
	policy         peerPolicy
	seq            uint64
	startedAt      time.Time
	trustEmptyPeer bool

	BackupDir   string
	ClaudePath  string
	CodexPath   string
	HermesHome  string
	GatewayAddr string

	// BrowserRuntime manages child browser processes.
	BrowserRuntime *browser.Runtime
	Scheduler      *checkin.Scheduler

	// CLISync performs CLI configuration synchronisation. When nil the
	// management API reports the feature unsupported rather than pretending a
	// sync happened.
	CLISync *clisync.Service

	Verify  *verify.Registry
	Lab     *lab.Ring
	Sticky  *affinity.Table
	Limiter *ratelimit.Limiter
}

type peerPolicy struct {
	localOnly bool
	allowlist []netip.Prefix
	limit     RateLimitConfig
	mu        sync.Mutex
	windows   map[netip.Addr]rateWindow
}

type rateWindow struct {
	started time.Time
	count   int
}

type RateLimitConfig struct {
	Requests int
	Window   time.Duration
}

// NewServer preserves the small constructor used by the bootstrap tests. It
// has no persistence, but still serves safe read-only status surfaces.
func NewServer(token string) *Server {
	return &Server{Token: token, LocalOnly: true, Management: "127.0.0.1:8790", startedAt: time.Now().UTC(), trustEmptyPeer: true}
}

// Config makes local-only enforcement explicit at construction time.
type Config struct {
	Token          string
	Repo           repository.ResourceRepository
	LocalOnly      bool
	Management     string
	StartedAt      time.Time
	SecretStore    *secrets.Store
	Logger         *logging.Logger
	AuditLogger    *audit.Logger
	LocalKeys      *auth.LocalKeyService
	OnConfigChange func(context.Context) error
	AllowedCIDRs   []string
	AllowedPeers   []string
	RateLimit      RateLimitConfig
	// Listener indicates that the server will be mounted through the serving
	// boundary. Direct Handler use remains available for local in-process tests.
	Listener net.Listener

	// CLISync / ImportExport configuration
	BackupDir   string
	ClaudePath  string
	CodexPath   string
	HermesHome  string
	GatewayAddr string

	BrowserRuntime *browser.Runtime
	Scheduler      *checkin.Scheduler
}

func NewConfiguredServer(cfg Config) (*Server, error) {
	if cfg.Management == "" {
		cfg.Management = "127.0.0.1:8790"
		cfg.LocalOnly = true
	}
	if cfg.LocalOnly && !isLoopbackAddr(cfg.Management) {
		return nil, fmt.Errorf("management address is not loopback")
	}
	var allowlist []netip.Prefix
	for _, raw := range append(append([]string(nil), cfg.AllowedCIDRs...), cfg.AllowedPeers...) {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid management peer allowlist entry")
		}
		allowlist = append(allowlist, prefix)
	}
	if !cfg.LocalOnly {
		if cfg.Token == "" {
			return nil, fmt.Errorf("remote management requires a bearer token")
		}
		if len(allowlist) == 0 {
			return nil, fmt.Errorf("remote management requires an explicit peer allowlist")
		}
		if cfg.RateLimit.Requests <= 0 || cfg.RateLimit.Window <= 0 {
			return nil, fmt.Errorf("remote management requires a positive rate limit")
		}
	}
	if cfg.StartedAt.IsZero() {
		cfg.StartedAt = time.Now().UTC()
	}
	logger := cfg.Logger
	if logger == nil {
		logger = logging.New(io.Discard)
	}

	// Build the CLI sync service from the configured paths. Each syncer resolves
	// its own default location when the path is empty, so tests can redirect
	// writes into a temp dir without special-casing production behaviour.
	gatewayBase := cfg.GatewayAddr
	if gatewayBase == "" {
		gatewayBase = "127.0.0.1:8789"
	}
	syncEngine := clisync.NewEngine(cfg.Repo, cfg.BackupDir)
	cliSyncSvc := clisync.NewService(syncEngine, cfg.LocalKeys, gatewayBase, map[string]clisync.Syncer{
		"claude": &claude.Syncer{Engine: syncEngine, Path: cfg.ClaudePath, GatewayAddr: gatewayBase},
		"codex":  &codex.Syncer{Engine: syncEngine, Path: cfg.CodexPath, GatewayAddr: gatewayBase},
		"hermes": &hermes.Syncer{Engine: syncEngine, Home: cfg.HermesHome, GatewayAddr: gatewayBase},
	})

	return &Server{
		Token:          cfg.Token,
		Repo:           cfg.Repo,
		LocalOnly:      cfg.LocalOnly,
		Management:     cfg.Management,
		SecretStore:    cfg.SecretStore,
		Logger:         logger,
		AuditLogger:    cfg.AuditLogger,
		LocalKeys:      cfg.LocalKeys,
		OnConfigChange: cfg.OnConfigChange,
		policy:         peerPolicy{localOnly: cfg.LocalOnly, allowlist: allowlist, limit: cfg.RateLimit, windows: make(map[netip.Addr]rateWindow)},
		startedAt:      cfg.StartedAt,
		trustEmptyPeer: cfg.LocalOnly,
		BackupDir:      cfg.BackupDir,
		ClaudePath:     cfg.ClaudePath,
		CodexPath:      cfg.CodexPath,
		HermesHome:     cfg.HermesHome,
		GatewayAddr:    cfg.GatewayAddr,
		BrowserRuntime: cfg.BrowserRuntime,
		Scheduler:      cfg.Scheduler,
		CLISync:        cliSyncSvc,
	}, nil
}

// NewServerOnListener binds management peer checks to the listener boundary.
func NewServerOnListener(listener net.Listener, cfg Config) (*http.Server, error) {
	if listener == nil {
		return nil, fmt.Errorf("management listener is required")
	}
	s, err := NewConfiguredServer(cfg)
	if err != nil {
		return nil, err
	}
	if s.LocalOnly && !listenerIsLoopback(listener) {
		return nil, fmt.Errorf("management listener is not loopback")
	}
	return &http.Server{Handler: s.listenerHandler()}, nil
}

func listenerIsLoopback(listener net.Listener) bool {
	addr, ok := listener.Addr().(*net.TCPAddr)
	return ok && addr.IP != nil && addr.IP.IsLoopback()
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr == "localhost"
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]" {
		return true
	}
	return net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func (s *Server) Handler() http.Handler {
	if !s.LocalOnly {
		return requestIDMiddleware(s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.fail(w, r, fault{status: http.StatusServiceUnavailable, code: "service_unavailable", message: "management listener policy is required"})
		}))
	}
	if s.trustEmptyPeer {
		return requestIDMiddleware(s, s.managementRoutes())
	}
	return s.listenerHandler()
}

func (s *Server) listenerHandler() http.Handler {
	return requestIDMiddleware(s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.peerAllowed(w, r) {
			return
		}
		s.managementRoutes().ServeHTTP(w, r)
	}))
}

func (s *Server) managementRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/api/v1/health", s.health)
	mux.HandleFunc("/api/v1/overview", s.overview)
	mux.HandleFunc("/api/v1/providers", s.providers)
	mux.HandleFunc("/api/v1/providers/", s.providers)
	mux.HandleFunc("/api/v1/channels", s.channels)
	mux.HandleFunc("/api/v1/channels/", s.channels)
	mux.HandleFunc("/api/v1/models", s.models)
	mux.HandleFunc("/api/v1/models/", s.models)
	mux.HandleFunc("/api/v1/provider-models", s.providerModels)
	mux.HandleFunc("/api/v1/provider-models/", s.providerModels)
	mux.HandleFunc("/api/v1/model-groups", s.modelGroups)
	mux.HandleFunc("/api/v1/model-groups/", s.modelGroups)
	mux.HandleFunc("/api/v1/routes", s.routeHandler)
	mux.HandleFunc("/api/v1/routes/", s.routeHandler)
	mux.HandleFunc("/api/v1/checkin", s.checkin)
	mux.HandleFunc("/api/v1/logs", s.logs)
	mux.HandleFunc("/api/v1/usage", s.usage)
	mux.HandleFunc("/api/v1/settings", s.settings)
	mux.HandleFunc("/api/v1/browser", s.browserHandler)
	mux.HandleFunc("/api/v1/cli-sync", s.cliSync)
	mux.HandleFunc("/api/v1/import-export", s.importExport)
	mux.HandleFunc("/api/v1/secrets", s.secrets)
	mux.HandleFunc("/api/v1/keys", s.keys)
	mux.HandleFunc("/api/v1/keys/", s.keys)
	mux.HandleFunc("/api/v1/secrets/", s.secrets)
	mux.HandleFunc("/api/v1/fetch-models", s.fetchModels)
	mux.HandleFunc("/api/v1/channels/sync-models", s.channelSyncModels)
	mux.HandleFunc("/api/v1/channels/test", s.channelTest)
	mux.HandleFunc("/api/v1/models/catalog", s.modelCatalog)
	mux.HandleFunc("/api/v1/models/batch", s.modelBatch)
	mux.HandleFunc("/api/v1/channels/duplicate", s.channelDuplicate)
	mux.HandleFunc("/api/v1/channels/batch", s.channelBatch)
	mux.HandleFunc("/api/v1/channel-keys", s.channelKeys)
	mux.HandleFunc("/api/v1/channel-keys/", s.channelKeys)
	mux.HandleFunc("/api/v1/channels/stats", s.channelStats)
	mux.Handle("/", http.FileServer(http.FS(webassets.Assets)))
	return s.browserBoundary(mux)
}

func (s *Server) peerAllowed(w http.ResponseWriter, r *http.Request) bool {
	ip, ok := requestPeerIP(r)
	if !ok {
		if s.trustEmptyPeer && strings.TrimSpace(r.RemoteAddr) == "" {
			return true
		}
		s.fail(w, r, forbidden("management peer is not allowed"))
		return false
	}
	if s.LocalOnly {
		if !ip.IsLoopback() {
			s.fail(w, r, forbidden("management peer is not loopback"))
			return false
		}
		return true
	}
	allowed := false
	for _, prefix := range s.policy.allowlist {
		if prefix.Contains(ip) {
			allowed = true
			break
		}
	}
	if !allowed {
		s.fail(w, r, forbidden("management peer is not allowed"))
		return false
	}
	if !s.rateAllowed(ip, time.Now()) {
		s.fail(w, r, fault{status: http.StatusTooManyRequests, code: "rate_limit_exceeded", message: "management rate limit exceeded"})
		return false
	}
	return true
}

func requestPeerIP(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(host)
	return ip, err == nil
}

func (s *Server) rateAllowed(ip netip.Addr, now time.Time) bool {
	if s.policy.limit.Requests <= 0 || s.policy.limit.Window <= 0 {
		return false
	}
	s.policy.mu.Lock()
	defer s.policy.mu.Unlock()
	window := s.policy.windows[ip]
	if window.started.IsZero() || now.Sub(window.started) >= s.policy.limit.Window {
		window = rateWindow{started: now}
	}
	if window.count >= s.policy.limit.Requests {
		s.policy.windows[ip] = window
		return false
	}
	window.count++
	s.policy.windows[ip] = window
	return true
}

type requestIDKey struct{}

func requestIDMiddleware(s *Server, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, s.id(r)))
		next.ServeHTTP(w, r)
	})
}

func (s *Server) id(r *http.Request) string {
	if x := strings.TrimSpace(r.Header.Get("X-Request-ID")); x != "" && len(x) <= 128 {
		return x
	}
	return "req-" + strconv.FormatUint(atomic.AddUint64(&s.seq, 1), 10)
}
func requestID(r *http.Request) string {
	if id, ok := r.Context().Value(requestIDKey{}).(string); ok {
		return id
	}
	return "req-unknown"
}
func (s *Server) write(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID(r))
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if f, ok := err.(fault); ok && f.cause != nil && s.Logger != nil {
		_ = s.Logger.Event("error", "management_internal_error", requestID(r), map[string]any{"error": f.cause.Error()})
	}
	encodeError(w, requestID(r), err)
}
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if !s.healthRequestAllowed(w, r) {
		return
	}
	s.write(w, r, http.StatusOK, map[string]any{"status": "ok"})
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if !s.healthRequestAllowed(w, r) {
		return
	}
	s.write(w, r, http.StatusOK, map[string]any{"status": "ok", "management": s.Management, "local_only": s.LocalOnly, "request_id": requestID(r)})
}
func (s *Server) healthRequestAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !s.authorize(w, r) {
		return false
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	s.fail(w, r, badRequest("method_not_allowed", "GET is required"))
	return false
}
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		s.fail(w, r, badRequest("method_not_allowed", "GET is required"))
		return
	}
	counts := map[string]int{}
	if s.Repo != nil {
		ctx := r.Context()
		if v, err := s.Repo.ListProviders(ctx); err == nil {
			counts["providers"] = len(v)
		}
		if v, err := s.Repo.ListChannels(ctx, ""); err == nil {
			counts["channels"] = len(v)
		}
		if v, err := s.Repo.ListModels(ctx); err == nil {
			counts["models"] = len(v)
		}
		if v, err := s.Repo.ListRoutes(ctx); err == nil {
			counts["routes"] = len(v)
		}
	}
	s.write(w, r, http.StatusOK, map[string]any{"status": "ok", "uptime_seconds": int(time.Since(s.startedAt).Seconds()), "counts": counts})
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	// Loopback local mode without a configured token: allow everything.
	// The listener/peer policy already guarantees the peer is loopback.
	if s.LocalOnly && s.Token == "" {
		return true
	}
	if s.LocalOnly && (r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions) {
		return true
	}
	if s.Token == "" {
		s.fail(w, r, unauthorized())
		return false
	}
	want := "Bearer " + s.Token
	if subtleEqual(r.Header.Get("Authorization"), want) {
		return true
	}
	s.fail(w, r, unauthorized())
	return false
}
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func (s *Server) notifyConfigChange(ctx context.Context) {
	if s.OnConfigChange != nil {
		_ = s.OnConfigChange(ctx)
	}
}

func (s *Server) auditEvent(ctx context.Context, action string, r *http.Request, metadata map[string]string) {
	if s.AuditLogger == nil {
		return
	}
	_ = s.AuditLogger.Record(ctx, audit.Event{
		Action:    action,
		RequestID: requestID(r),
		Actor:     r.RemoteAddr,
		Metadata:  metadata,
	})
}

// fetchModels fetches the model list from an upstream (AxonHub-style server-side fetch).
// Input: {channel_id} for an existing channel, or {channel_type, base_url, api_key} for a draft channel.
func (s *Server) fetchModels(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		s.fail(w, r, badRequest("method_not_allowed", "POST is required"))
		return
	}
	var input struct {
		ChannelID   string `json:"channel_id"`
		ChannelType string `json:"channel_type"`
		BaseURL     string `json:"base_url"`
		APIKey      string `json:"api_key"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}

	baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	apiKey := input.APIKey

	// Existing channel: resolve base URL and credential from the store.
	if input.ChannelID != "" {
		if s.Repo == nil {
			s.fail(w, r, unavailable("resource persistence is not configured"))
			return
		}
		ch, err := s.Repo.GetChannel(r.Context(), input.ChannelID)
		if err != nil {
			s.fail(w, r, mapRepoError(err, "channel"))
			return
		}
		baseURL = strings.TrimRight(ch.BaseURL, "/")
		// Resolve the credential the same way the gateway/test/sync paths do:
		// prefer an enabled per-channel key, fall back to the legacy CredentialRef.
		// Reading only CredentialRef here left UI-added keys invisible (401 upstream).
		if s.SecretStore != nil {
			if keys, kerr := s.Repo.ListChannelKeys(r.Context(), input.ChannelID); kerr == nil {
				for _, k := range keys {
					if k.Disabled {
						continue
					}
					if secret, gerr := s.SecretStore.Get(r.Context(), k.SecretRef); gerr == nil {
						apiKey = string(secret)
						break
					}
				}
			}
			if apiKey == "" && ch.CredentialRef != "" {
				if secret, err := s.SecretStore.Get(r.Context(), ch.CredentialRef); err == nil {
					apiKey = string(secret)
				}
			}
		}
	}

	if baseURL == "" {
		s.fail(w, r, badRequest("validation_error", "base_url or channel_id is required"))
		return
	}

	// Build the upstream /models endpoint. Prefer /v1/models; fall back to /models.
	client := &http.Client{Timeout: 20 * time.Second}
	endpoints := []string{baseURL + "/v1/models", baseURL + "/models"}
	var lastErr error
	for _, ep := range endpoints {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, ep, nil)
		if err != nil {
			s.fail(w, r, internal(err))
			return
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("network error connecting to %s: %v", ep, err)
			if s.Logger != nil {
				_ = s.Logger.Event("warn", "fetch_models_network_error", "", map[string]any{"endpoint": ep, "error": err.Error()})
			}
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("upstream %s returned %d", ep, resp.StatusCode)
			if s.Logger != nil {
				_ = s.Logger.Event("warn", "fetch_models_http_error", "", map[string]any{"endpoint": ep, "status": resp.StatusCode})
			}
			continue
		}
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			lastErr = fmt.Errorf("upstream %s returned malformed JSON: %v", ep, err)
			if s.Logger != nil {
				_ = s.Logger.Event("warn", "fetch_models_parse_error", "", map[string]any{"endpoint": ep, "error": err.Error()})
			}
			continue
		}
		models := make([]string, 0, len(payload.Data))
		for _, m := range payload.Data {
			if m.ID != "" {
				models = append(models, m.ID)
			}
		}
		s.write(w, r, http.StatusOK, map[string]any{"models": models})
		return
	}
	errMsg := "failed to fetch models from upstream"
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	s.write(w, r, http.StatusBadGateway, map[string]any{"models": []string{}, "error": map[string]any{"code": "provider_unavailable", "message": errMsg}})
}

// channelSyncModels fetches the upstream model list, applies optional regex pattern,
// and updates the channel's provider-model bindings (AxonHub syncChannelModels).
func (s *Server) channelSyncModels(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		s.fail(w, r, badRequest("method_not_allowed", "POST is required"))
		return
	}
	var input struct {
		ChannelID string `json:"channel_id"`
		Pattern   string `json:"pattern"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	if input.ChannelID == "" {
		s.fail(w, r, badRequest("validation_error", "channel_id is required"))
		return
	}
	filtered, created, err := s.syncChannelModels(r.Context(), input.ChannelID, input.Pattern)
	if err != nil {
		if errors.Is(err, errInvalidPattern) {
			s.fail(w, r, badRequest("validation_error", err.Error()))
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			s.fail(w, r, mapRepoError(err, "channel"))
			return
		}
		s.fail(w, r, fault{status: http.StatusBadGateway, code: "provider_unavailable", message: err.Error()})
		return
	}
	s.auditEvent(r.Context(), "sync_channel_models", r, map[string]string{"channel": input.ChannelID, "models": fmt.Sprintf("%d", created)})
	s.notifyConfigChange(r.Context())
	s.write(w, r, http.StatusOK, map[string]any{
		"channel_id": input.ChannelID,
		"models":     filtered,
		"total":      len(filtered),
	})
}

var errInvalidPattern = errors.New("invalid pattern")

// syncChannelModels fetches the upstream model list for a channel, applies an
// optional regex pattern, and rebuilds the channel's provider-model bindings.
// Extracted so both the HTTP handler and the scheduler auto-sync callback share
// one implementation. Returns the surviving model names and the count bound.
func (s *Server) syncChannelModels(ctx context.Context, channelID, pattern string) ([]string, int, error) {
	if s.Repo == nil {
		return nil, 0, errors.New("resource persistence is not configured")
	}
	ch, err := s.Repo.GetChannel(ctx, channelID)
	if err != nil {
		return nil, 0, err
	}
	apiKey := ""
	if s.SecretStore != nil {
		if keys, kerr := s.Repo.ListChannelKeys(ctx, channelID); kerr == nil {
			for _, k := range keys {
				if k.Disabled {
					continue
				}
				if secret, gerr := s.SecretStore.Get(ctx, k.SecretRef); gerr == nil {
					apiKey = string(secret)
					break
				}
			}
		}
		if apiKey == "" && ch.CredentialRef != "" {
			if secret, gerr := s.SecretStore.Get(ctx, ch.CredentialRef); gerr == nil {
				apiKey = string(secret)
			}
		}
	}
	models, err := s.fetchUpstreamModelList(ctx, ch.BaseURL, apiKey)
	if err != nil {
		return nil, 0, err
	}
	filtered := models
	if pattern != "" {
		re, reErr := regexp.Compile(pattern)
		if reErr != nil {
			return nil, 0, fmt.Errorf("%w: %s", errInvalidPattern, reErr.Error())
		}
		filtered = filtered[:0]
		for _, m := range models {
			if re.MatchString(m) {
				filtered = append(filtered, m)
			}
		}
	}
	existing, _ := s.Repo.ListProviderModels(ctx, "")
	for _, pm := range existing {
		if pm.ChannelID == channelID {
			_ = s.Repo.DeleteProviderModel(ctx, pm.ID)
		}
	}
	created := 0
	for _, m := range filtered {
		_ = s.Repo.CreateModel(ctx, domain.Model{ID: m, DisplayName: m, Enabled: true})
		pm := domain.ProviderModel{
			ID:                fmt.Sprintf("pm-%s-%s", channelID, m),
			ProviderID:        ch.ProviderID,
			ChannelID:         channelID,
			ModelID:           m,
			UpstreamModelName: m,
			Protocol:          "openai-chat",
			Priority:          ch.Priority,
			Weight:            ch.Weight,
			Enabled:           true,
		}
		if err := s.Repo.CreateProviderModel(ctx, pm); err == nil {
			created++
		}
	}
	return filtered, created, nil
}

// SyncChannelModels is the exported entry the scheduler's auto-sync callback
// uses; it discards the model list and reports only an error.
func (s *Server) SyncChannelModels(ctx context.Context, channelID, pattern string) error {
	_, _, err := s.syncChannelModels(ctx, channelID, pattern)
	return err
}

// modelCatalog aggregates the model list with provider-model binding counts.
// GET /api/v1/models/catalog -> 200 { "catalog": [ {model fields..., "binding_count": N} ] }
func (s *Server) modelCatalog(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		s.fail(w, r, badRequest("method_not_allowed", "GET is required"))
		return
	}
	if s.Repo == nil {
		s.write(w, r, http.StatusOK, map[string]any{"catalog": []any{}})
		return
	}
	ctx := r.Context()
	models, err := s.Repo.ListModels(ctx)
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	bindings, err := s.Repo.ListProviderModels(ctx, "")
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	counts := map[string]int{}
	for _, pm := range bindings {
		counts[pm.ModelID]++
	}
	includeArchived := r.URL.Query().Get("include_archived") == "true"
	catalog := make([]map[string]any, 0, len(models))
	for _, m := range models {
		if m.Archived && !includeArchived {
			continue
		}
		catalog = append(catalog, map[string]any{
			"id": m.ID, "display_name": m.DisplayName, "developer": m.Developer,
			"model_type": m.ModelType, "input_price": m.InputPrice, "output_price": m.OutputPrice,
			"reasoning_support": m.ReasoningSupport, "vision_support": m.VisionSupport,
			"tool_call_support": m.ToolCallSupport, "enabled": m.Enabled,
			"icon_url": m.IconURL, "archived": m.Archived,
			"binding_count": counts[m.ID],
		})
	}
	s.write(w, r, http.StatusOK, map[string]any{"catalog": catalog})
}

// modelBatch creates many models at once or bulk enable/disable/delete by ID.
// POST /api/v1/models/batch
//
//	{"models":[ {model}, ... ]}                  -> creates each (idempotent skip on duplicate)
//	{"action":"enable|disable|delete","ids":[]}  -> mutates each existing model
func (s *Server) modelBatch(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		s.fail(w, r, badRequest("method_not_allowed", "POST is required"))
		return
	}
	if s.Repo == nil {
		s.fail(w, r, unavailable("resource persistence is not configured"))
		return
	}
	var input struct {
		Models []domain.Model `json:"models"`
		Action string         `json:"action"`
		IDs    []string       `json:"ids"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	if len(input.Models) > 0 {
		created := 0
		var failed []string
		for _, m := range input.Models {
			if e := validateModel(m); e != nil {
				failed = append(failed, m.ID)
				continue
			}
			if e := s.Repo.CreateModel(ctx, m); e != nil {
				failed = append(failed, m.ID)
				continue
			}
			created++
		}
		s.auditEvent(ctx, "model_batch_create", r, map[string]string{"created": fmt.Sprintf("%d", created)})
		s.notifyConfigChange(ctx)
		s.write(w, r, http.StatusOK, map[string]any{"created": created, "failed": failed})
		return
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "enable", "disable", "archive", "unarchive", "delete":
	default:
		s.fail(w, r, badRequest("validation_error", "action must be enable, disable, archive, unarchive, or delete, or provide models[]"))
		return
	}
	if len(input.IDs) == 0 {
		s.fail(w, r, badRequest("validation_error", "ids is required for bulk actions"))
		return
	}
	affected := 0
	var failed []string
	for _, id := range input.IDs {
		var e error
		if action == "delete" {
			e = s.Repo.DeleteModel(ctx, id)
		} else {
			m, ge := s.Repo.GetModel(ctx, id)
			if ge != nil {
				failed = append(failed, id)
				continue
			}
			switch action {
			case "enable":
				m.Enabled = true
			case "disable":
				m.Enabled = false
			case "archive":
				m.Archived, m.Enabled = true, false
			case "unarchive":
				m.Archived = false
			}
			e = s.Repo.UpdateModel(ctx, m)
		}
		if e != nil {
			failed = append(failed, id)
			continue
		}
		affected++
	}
	s.auditEvent(ctx, "model_batch_"+action, r, map[string]string{"affected": fmt.Sprintf("%d", affected)})
	s.notifyConfigChange(ctx)
	s.write(w, r, http.StatusOK, map[string]any{"action": action, "affected": affected, "failed": failed})
}

// channelDuplicate clones an existing channel under a new ID/name.
// POST /api/v1/channels/duplicate {"channel_id":"c","new_id":"c2","new_name":"Copy"}
// The clone starts disabled so the operator can adjust credentials before enabling.
func (s *Server) channelDuplicate(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		s.fail(w, r, badRequest("method_not_allowed", "POST is required"))
		return
	}
	if s.Repo == nil {
		s.fail(w, r, unavailable("resource persistence is not configured"))
		return
	}
	var input struct {
		ChannelID string `json:"channel_id"`
		NewID     string `json:"new_id"`
		NewName   string `json:"new_name"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(input.ChannelID) == "" || strings.TrimSpace(input.NewID) == "" {
		s.fail(w, r, badRequest("validation_error", "channel_id and new_id are required"))
		return
	}
	ctx := r.Context()
	src, err := s.Repo.GetChannel(ctx, input.ChannelID)
	if err != nil {
		s.fail(w, r, mapRepoError(err, "channel"))
		return
	}
	clone := src
	clone.ID = input.NewID
	if strings.TrimSpace(input.NewName) != "" {
		clone.Name = input.NewName
	} else {
		clone.Name = src.Name + " (copy)"
	}
	// A clone must not silently reuse the source credential or go live unreviewed.
	clone.CredentialRef = ""
	clone.Enabled = false
	clone.Status = "disabled"
	clone.CreatedAt = time.Time{}
	clone.UpdatedAt = time.Time{}
	if e := s.Repo.CreateChannel(ctx, clone); e != nil {
		s.fail(w, r, mapRepoError(e, "channel"))
		return
	}
	s.auditEvent(ctx, "duplicate_channel", r, map[string]string{"from": input.ChannelID, "to": clone.ID})
	s.notifyConfigChange(ctx)
	s.write(w, r, http.StatusOK, map[string]any{"channel": safeChannel(clone)})
}

// channelBatch performs bulk enable/disable/archive/delete on channels by ID.
// POST /api/v1/channels/batch {"action":"enable|disable|archive|delete","ids":[...]}
func (s *Server) channelBatch(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		s.fail(w, r, badRequest("method_not_allowed", "POST is required"))
		return
	}
	if s.Repo == nil {
		s.fail(w, r, unavailable("resource persistence is not configured"))
		return
	}
	var input struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "enable", "disable", "archive", "delete":
	default:
		s.fail(w, r, badRequest("validation_error", "action must be enable, disable, archive, or delete"))
		return
	}
	if len(input.IDs) == 0 {
		s.fail(w, r, badRequest("validation_error", "ids is required"))
		return
	}
	ctx := r.Context()
	affected := 0
	var failed []string
	for _, id := range input.IDs {
		var e error
		if action == "delete" {
			e = s.Repo.DeleteChannel(ctx, id)
		} else {
			c, ge := s.Repo.GetChannel(ctx, id)
			if ge != nil {
				failed = append(failed, id)
				continue
			}
			switch action {
			case "enable":
				c.Enabled, c.Status = true, "enabled"
			case "disable":
				c.Enabled, c.Status = false, "disabled"
			case "archive":
				c.Enabled, c.Status = false, "archived"
			}
			e = s.Repo.UpdateChannel(ctx, c)
		}
		if e != nil {
			failed = append(failed, id)
			continue
		}
		affected++
	}
	s.auditEvent(ctx, "channel_batch_"+action, r, map[string]string{"affected": fmt.Sprintf("%d", affected)})
	s.notifyConfigChange(ctx)
	s.write(w, r, http.StatusOK, map[string]any{"action": action, "affected": affected, "failed": failed})
}

// channelKeys manages per-channel API keys. The secret value is written to the
// encrypted secret store; only metadata (id/label/disabled) is ever returned.
//
//	GET    /api/v1/channel-keys?channel_id=c      -> {"keys":[{id,label,disabled,...}]}  (never the secret)
//	POST   /api/v1/channel-keys {channel_id,value,label}   -> creates key, seals value
//	PATCH  /api/v1/channel-keys/{id} {disabled,label}      -> toggle/rename
//	DELETE /api/v1/channel-keys/{id}                       -> removes metadata + sealed secret
func (s *Server) channelKeys(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if s.Repo == nil {
		if r.Method == http.MethodGet {
			s.write(w, r, http.StatusOK, map[string]any{"keys": []any{}})
			return
		}
		s.fail(w, r, unavailable("resource persistence is not configured"))
		return
	}
	ctx := r.Context()
	id := strings.Trim(strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/v1/channel-keys")), "/")
	if id != "" && strings.Contains(id, "/") {
		s.fail(w, r, badRequest("invalid_id", "key ID must be one path segment"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		keys, err := s.Repo.ListChannelKeys(ctx, r.URL.Query().Get("channel_id"))
		if err != nil {
			s.fail(w, r, internal(err))
			return
		}
		s.write(w, r, http.StatusOK, map[string]any{"keys": safeChannelKeys(keys)})
	case http.MethodPost:
		if s.SecretStore == nil {
			s.fail(w, r, unavailable("secret store is not configured; cannot store key material"))
			return
		}
		var input struct {
			ChannelID string `json:"channel_id"`
			Value     string `json:"value"`
			Label     string `json:"label"`
		}
		if err := decodeJSON(w, r, &input); err != nil {
			s.fail(w, r, err)
			return
		}
		if strings.TrimSpace(input.ChannelID) == "" || strings.TrimSpace(input.Value) == "" {
			s.fail(w, r, badRequest("validation_error", "channel_id and value are required"))
			return
		}
		if _, err := s.Repo.GetChannel(ctx, input.ChannelID); err != nil {
			s.fail(w, r, mapRepoError(err, "channel"))
			return
		}
		keyID := "chk-" + newID()
		secretRef := "chkey:" + input.ChannelID + ":" + keyID
		if err := s.SecretStore.Put(ctx, secretRef, []byte(input.Value)); err != nil {
			s.fail(w, r, internal(err))
			return
		}
		k := domain.ChannelKey{ID: keyID, ChannelID: input.ChannelID, SecretRef: secretRef, Label: strings.TrimSpace(input.Label)}
		if err := s.Repo.CreateChannelKey(ctx, k); err != nil {
			// Roll back the sealed secret so we don't orphan key material.
			_ = s.SecretStore.Delete(ctx, secretRef)
			s.fail(w, r, mapRepoError(err, "channel key"))
			return
		}
		s.auditEvent(ctx, "create_channel_key", r, map[string]string{"channel": input.ChannelID, "key": keyID})
		s.notifyConfigChange(ctx)
		s.write(w, r, http.StatusCreated, map[string]any{"key": safeChannelKey(k)})
	case http.MethodPatch, http.MethodPut:
		if id == "" {
			s.fail(w, r, badRequest("invalid_id", "key ID is required"))
			return
		}
		existing, err := s.Repo.GetChannelKey(ctx, id)
		if err != nil {
			s.fail(w, r, mapRepoError(err, "channel key"))
			return
		}
		var input struct {
			Disabled *bool   `json:"disabled"`
			Label    *string `json:"label"`
		}
		if err := decodeJSON(w, r, &input); err != nil {
			s.fail(w, r, err)
			return
		}
		if input.Disabled != nil {
			existing.Disabled = *input.Disabled
		}
		if input.Label != nil {
			existing.Label = strings.TrimSpace(*input.Label)
		}
		if err := s.Repo.UpdateChannelKey(ctx, existing); err != nil {
			s.fail(w, r, mapRepoError(err, "channel key"))
			return
		}
		s.auditEvent(ctx, "update_channel_key", r, map[string]string{"key": id})
		s.notifyConfigChange(ctx)
		s.write(w, r, http.StatusOK, map[string]any{"key": safeChannelKey(existing)})
	case http.MethodDelete:
		if id == "" {
			s.fail(w, r, badRequest("invalid_id", "key ID is required"))
			return
		}
		existing, err := s.Repo.GetChannelKey(ctx, id)
		if err != nil {
			s.fail(w, r, mapRepoError(err, "channel key"))
			return
		}
		if err := s.Repo.DeleteChannelKey(ctx, id); err != nil {
			s.fail(w, r, mapRepoError(err, "channel key"))
			return
		}
		if s.SecretStore != nil && existing.SecretRef != "" {
			_ = s.SecretStore.Delete(ctx, existing.SecretRef)
		}
		s.auditEvent(ctx, "delete_channel_key", r, map[string]string{"key": id})
		s.notifyConfigChange(ctx)
		s.write(w, r, http.StatusOK, map[string]any{"deleted": id})
	default:
		s.fail(w, r, badRequest("method_not_allowed", "unsupported channel key method"))
	}
}

// newID returns a short random hex identifier for server-minted records.
func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read never fails on supported platforms; fall back to time-based
		// uniqueness rather than panicking in a request path.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// safeChannelKey strips any secret-bearing field, returning only metadata.
func safeChannelKey(k domain.ChannelKey) map[string]any {
	return map[string]any{
		"id": k.ID, "channel_id": k.ChannelID, "label": k.Label,
		"disabled": k.Disabled, "created_at": k.CreatedAt,
	}
}
func safeChannelKeys(keys []domain.ChannelKey) []map[string]any {
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, safeChannelKey(k))
	}
	return out
}

// channelStats aggregates per-channel health, quota, recent request outcomes and
// key counts for the expanded-row view.
//
//	GET /api/v1/channels/stats?channel_id=c -> {stats:{channel_id, health_state, quota_state, error_message, key_count, enabled_key_count, recent_requests, recent_success, recent_errors, last_checked_at, avg_latency_ms}}
func (s *Server) channelStats(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		s.fail(w, r, badRequest("method_not_allowed", "GET is required"))
		return
	}
	if s.Repo == nil {
		s.fail(w, r, unavailable("resource persistence is not configured"))
		return
	}
	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))
	if channelID == "" {
		s.fail(w, r, badRequest("validation_error", "channel_id is required"))
		return
	}
	ctx := r.Context()
	ch, err := s.Repo.GetChannel(ctx, channelID)
	if err != nil {
		s.fail(w, r, mapRepoError(err, "channel"))
		return
	}
	keys, _ := s.Repo.ListChannelKeys(ctx, channelID)
	enabledKeys := 0
	for _, k := range keys {
		if !k.Disabled {
			enabledKeys++
		}
	}
	// Recent request outcomes for this channel (bounded to the newest 100).
	recent, recentOK, recentErr, latencySum := 0, 0, 0, 0
	if reqs, err := s.Repo.ListRequestRecords(ctx); err == nil {
		for _, rec := range reqs {
			if rec.ChannelID != channelID {
				continue
			}
			if recent >= 100 {
				break
			}
			recent++
			latencySum += rec.LatencyMS
			if rec.StatusCode >= 200 && rec.StatusCode < 400 {
				recentOK++
			} else {
				recentErr++
			}
		}
	}
	avgLatency := 0
	if recent > 0 {
		avgLatency = latencySum / recent
	}
	var lastChecked any
	if h, err := s.Repo.ListHealthRecords(ctx, channelID); err == nil && len(h) > 0 {
		lastChecked = h[0].CheckedAt
	}
	s.write(w, r, http.StatusOK, map[string]any{"stats": map[string]any{
		"channel_id":        channelID,
		"health_state":      ch.HealthState,
		"quota_state":       ch.QuotaState,
		"rate_limit_state":  ch.RateLimitState,
		"error_message":     ch.ErrorMessage,
		"key_count":         len(keys),
		"enabled_key_count": enabledKeys,
		"recent_requests":   recent,
		"recent_success":    recentOK,
		"recent_errors":     recentErr,
		"avg_latency_ms":    avgLatency,
		"last_checked_at":   lastChecked,
	}})
}

// channelTest tests a channel's connectivity using its default_test_model (or first model).
func (s *Server) channelTest(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		s.fail(w, r, badRequest("method_not_allowed", "POST is required"))
		return
	}
	var input struct {
		ChannelID string `json:"channel_id"`
		KeyID     string `json:"key_id"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	if input.ChannelID == "" {
		s.fail(w, r, badRequest("validation_error", "channel_id is required"))
		return
	}
	ch, err := s.Repo.GetChannel(r.Context(), input.ChannelID)
	if err != nil {
		s.fail(w, r, mapRepoError(err, "channel"))
		return
	}

	// Fetch model list as connectivity test (AxonHub-style test via default model).
	// When key_id is given, test that single per-channel key; otherwise prefer the
	// channel's first enabled key, falling back to the legacy CredentialRef.
	apiKey := ""
	if s.SecretStore != nil {
		if input.KeyID != "" {
			k, kerr := s.Repo.GetChannelKey(r.Context(), input.KeyID)
			if kerr != nil || k.ChannelID != input.ChannelID {
				s.fail(w, r, notFound("channel key not found for this channel"))
				return
			}
			if secret, err := s.SecretStore.Get(r.Context(), k.SecretRef); err == nil {
				apiKey = string(secret)
			}
		} else {
			if keys, err := s.Repo.ListChannelKeys(r.Context(), input.ChannelID); err == nil {
				for _, k := range keys {
					if k.Disabled {
						continue
					}
					if secret, err := s.SecretStore.Get(r.Context(), k.SecretRef); err == nil {
						apiKey = string(secret)
						break
					}
				}
			}
			if apiKey == "" && ch.CredentialRef != "" {
				if secret, err := s.SecretStore.Get(r.Context(), ch.CredentialRef); err == nil {
					apiKey = string(secret)
				}
			}
		}
	}
	start := time.Now()
	models, err := s.fetchUpstreamModelList(r.Context(), ch.BaseURL, apiKey)
	latency := time.Since(start).Milliseconds()

	// Update channel error state
	if err != nil {
		ch.ErrorMessage = err.Error()
		ch.HealthState = "unhealthy"
		_ = s.Repo.UpdateChannel(r.Context(), ch)
		s.write(w, r, http.StatusOK, map[string]any{
			"channel_id": input.ChannelID,
			"success":    false,
			"latency_ms": latency,
			"error":      err.Error(),
		})
		return
	}
	ch.ErrorMessage = ""
	ch.HealthState = "healthy"
	_ = s.Repo.UpdateChannel(r.Context(), ch)
	s.auditEvent(r.Context(), "test_channel", r, map[string]string{"channel": input.ChannelID})
	s.write(w, r, http.StatusOK, map[string]any{
		"channel_id":         input.ChannelID,
		"success":            true,
		"latency_ms":         latency,
		"model_count":        len(models),
		"default_test_model": ch.DefaultTestModel,
	})
}

// fetchUpstreamModelList is a shared helper that GETs {base}/v1/models (fallback /models).
func (s *Server) fetchUpstreamModelList(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, errors.New("base_url is empty")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	for _, ep := range []string{base + "/v1/models", base + "/models"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep, nil)
		if err != nil {
			return nil, err
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			continue
		}
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			continue
		}
		out := make([]string, 0, len(payload.Data))
		for _, m := range payload.Data {
			if m.ID != "" {
				out = append(out, m.ID)
			}
		}
		return out, nil
	}
	return nil, errors.New("failed to fetch models from upstream")
}

func (s *Server) secrets(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		s.fail(w, r, badRequest("method_not_allowed", "POST or PUT is required"))
		return
	}
	if s.SecretStore == nil {
		s.fail(w, r, unavailable("secret store is not configured"))
		return
	}
	var input struct {
		Ref   string `json:"ref"`
		Value string `json:"value"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	input.Ref = strings.TrimSpace(input.Ref)
	if input.Ref == "" || input.Value == "" {
		s.fail(w, r, badRequest("validation_error", "secret ref and value are required"))
		return
	}
	if len(input.Value) > 1<<20 {
		s.fail(w, r, badRequest("validation_error", "secret value is too large"))
		return
	}
	if err := s.SecretStore.Put(r.Context(), input.Ref, []byte(input.Value)); err != nil {
		s.fail(w, r, internal(err))
		return
	}
	s.auditEvent(r.Context(), "create_secret", r, map[string]string{"ref": input.Ref})
	s.write(w, r, http.StatusCreated, map[string]any{"secret": map[string]any{"ref": input.Ref, "stored": true}})
}

func (s *Server) providers(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	s.resource(w, r, "provider", strings.TrimPrefix(r.URL.Path, "/api/v1/providers"))
}
func (s *Server) channels(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	s.resource(w, r, "channel", strings.TrimPrefix(r.URL.Path, "/api/v1/channels"))
}
func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	s.resource(w, r, "model", strings.TrimPrefix(r.URL.Path, "/api/v1/models"))
}
func (s *Server) providerModels(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	s.resource(w, r, "provider_model", strings.TrimPrefix(r.URL.Path, "/api/v1/provider-models"))
}
func (s *Server) modelGroups(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	s.resource(w, r, "model_group", strings.TrimPrefix(r.URL.Path, "/api/v1/model-groups"))
}
func (s *Server) routeHandler(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	s.resource(w, r, "route", strings.TrimPrefix(r.URL.Path, "/api/v1/routes"))
}

func (s *Server) resource(w http.ResponseWriter, r *http.Request, kind, suffix string) {
	if s.Repo == nil {
		if r.Method == http.MethodGet {
			s.write(w, r, http.StatusOK, map[string]any{plural(kind): []any{}})
			return
		}
		s.fail(w, r, unavailable("resource persistence is not configured"))
		return
	}
	ctx := r.Context()
	id := strings.Trim(strings.TrimSpace(suffix), "/")
	if kind == "model_group" && id != "" && strings.HasSuffix(id, "/members") {
		id = strings.TrimSuffix(id, "/members")
		s.groupMembers(w, r, ctx, id)
		return
	}
	if id != "" && strings.Contains(id, "/") {
		s.fail(w, r, badRequest("invalid_id", "resource ID must be one path segment"))
		return
	}
	switch kind {
	case "provider":
		s.providerResource(w, r, ctx, id)
	case "channel":
		s.channelResource(w, r, ctx, id)
	case "model":
		s.modelResource(w, r, ctx, id)
	case "provider_model":
		s.providerModelResource(w, r, ctx, id)
	case "model_group":
		s.groupResource(w, r, ctx, id)
	case "route":
		s.routeResource(w, r, ctx, id)
	}
}
func plural(kind string) string {
	return map[string]string{"provider": "providers", "channel": "channels", "model": "models", "provider_model": "provider_models", "model_group": "model_groups", "route": "routes"}[kind]
}

func (s *Server) providerResource(w http.ResponseWriter, r *http.Request, ctx context.Context, id string) {
	switch r.Method {
	case http.MethodGet:
		if id == "" {
			v, err := s.Repo.ListProviders(ctx)
			if err != nil {
				s.fail(w, r, internal(err))
				return
			}
			s.write(w, r, 200, map[string]any{"providers": safeProviders(v)})
			return
		}
		v, err := s.Repo.GetProvider(ctx, id)
		if errors.Is(err, repository.ErrNotFound) {
			s.fail(w, r, notFound("provider not found"))
			return
		}
		if err != nil {
			s.fail(w, r, internal(err))
			return
		}
		s.write(w, r, 200, map[string]any{"provider": safeProvider(v)})
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		var p domain.Provider
		if err := decodeJSON(w, r, &p); err != nil {
			s.fail(w, r, err)
			return
		}
		if id != "" {
			p.ID = id
		}
		if err := validateProvider(p); err != nil {
			s.fail(w, r, err)
			return
		}
		var err error
		if r.Method == http.MethodPost && id == "" {
			err = s.Repo.CreateProvider(ctx, p)
		} else {
			err = s.Repo.UpdateProvider(ctx, p)
		}
		if err != nil {
			s.fail(w, r, mapRepoError(err, "provider"))
			return
		}
		s.auditEvent(ctx, "save_provider", r, map[string]string{"id": p.ID, "name": p.Name})
		s.notifyConfigChange(ctx)
		s.write(w, r, http.StatusOK, map[string]any{"provider": safeProvider(p)})
	case http.MethodDelete:
		if id == "" {
			s.fail(w, r, badRequest("invalid_id", "provider ID is required"))
			return
		}
		if err := s.Repo.DeleteProvider(ctx, id); err != nil {
			s.fail(w, r, mapRepoError(err, "provider"))
			return
		}
		s.auditEvent(ctx, "delete_provider", r, map[string]string{"id": id})
		s.notifyConfigChange(ctx)
		s.write(w, r, http.StatusOK, map[string]any{"deleted": id})
	default:
		s.fail(w, r, badRequest("method_not_allowed", "unsupported provider method"))
	}
}
func (s *Server) channelResource(w http.ResponseWriter, r *http.Request, ctx context.Context, id string) {
	switch r.Method {
	case http.MethodGet:
		if id == "" {
			v, e := s.Repo.ListChannels(ctx, r.URL.Query().Get("provider_id"))
			if e != nil {
				s.fail(w, r, internal(e))
				return
			}
			s.write(w, r, 200, map[string]any{"channels": safeChannels(v)})
			return
		}
		v, e := s.Repo.GetChannel(ctx, id)
		if errors.Is(e, repository.ErrNotFound) {
			s.fail(w, r, notFound("channel not found"))
			return
		}
		if e != nil {
			s.fail(w, r, internal(e))
			return
		}
		s.write(w, r, 200, map[string]any{"channel": safeChannel(v)})
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		var c domain.Channel
		if e := decodeJSON(w, r, &c); e != nil {
			s.fail(w, r, e)
			return
		}
		if id != "" {
			c.ID = id
		}
		if e := validateChannel(c); e != nil {
			s.fail(w, r, e)
			return
		}
		var e error
		if r.Method == http.MethodPost && id == "" {
			e = s.Repo.CreateChannel(ctx, c)
		} else {
			e = s.Repo.UpdateChannel(ctx, c)
		}
		if e != nil {
			s.fail(w, r, mapRepoError(e, "channel"))
			return
		}
		s.auditEvent(ctx, "save_channel", r, map[string]string{"id": c.ID, "name": c.Name})
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"channel": safeChannel(c)})
	case http.MethodDelete:
		if id == "" {
			s.fail(w, r, badRequest("invalid_id", "channel ID is required"))
			return
		}
		if e := s.Repo.DeleteChannel(ctx, id); e != nil {
			s.fail(w, r, mapRepoError(e, "channel"))
			return
		}
		s.auditEvent(ctx, "delete_channel", r, map[string]string{"id": id})
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"deleted": id})
	default:
		s.fail(w, r, badRequest("method_not_allowed", "unsupported channel method"))
	}
}
func (s *Server) modelResource(w http.ResponseWriter, r *http.Request, ctx context.Context, id string) {
	switch r.Method {
	case http.MethodGet:
		if id == "" {
			v, e := s.Repo.ListModels(ctx)
			if e != nil {
				s.fail(w, r, internal(e))
				return
			}
			s.write(w, r, 200, map[string]any{"models": v})
			return
		}
		v, e := s.Repo.GetModel(ctx, id)
		if errors.Is(e, repository.ErrNotFound) {
			s.fail(w, r, notFound("model not found"))
			return
		}
		if e != nil {
			s.fail(w, r, internal(e))
			return
		}
		s.write(w, r, 200, map[string]any{"model": v})
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		var m domain.Model
		if e := decodeJSON(w, r, &m); e != nil {
			s.fail(w, r, e)
			return
		}
		if id != "" {
			m.ID = id
		}
		if e := validateModel(m); e != nil {
			s.fail(w, r, e)
			return
		}
		var e error
		if r.Method == http.MethodPost && id == "" {
			e = s.Repo.CreateModel(ctx, m)
		} else {
			e = s.Repo.UpdateModel(ctx, m)
		}
		if e != nil {
			s.fail(w, r, mapRepoError(e, "model"))
			return
		}
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"model": m})
	case http.MethodDelete:
		if id == "" {
			s.fail(w, r, badRequest("invalid_id", "model ID is required"))
			return
		}
		if e := s.Repo.DeleteModel(ctx, id); e != nil {
			s.fail(w, r, mapRepoError(e, "model"))
			return
		}
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"deleted": id})
	default:
		s.fail(w, r, badRequest("method_not_allowed", "unsupported model method"))
	}
}
func (s *Server) providerModelResource(w http.ResponseWriter, r *http.Request, ctx context.Context, id string) {
	switch r.Method {
	case http.MethodGet:
		if id == "" {
			v, e := s.Repo.ListProviderModels(ctx, r.URL.Query().Get("model_id"))
			if e != nil {
				s.fail(w, r, internal(e))
				return
			}
			s.write(w, r, 200, map[string]any{"provider_models": v})
			return
		}
		v, e := s.Repo.GetProviderModel(ctx, id)
		if errors.Is(e, repository.ErrNotFound) {
			s.fail(w, r, notFound("provider model not found"))
			return
		}
		if e != nil {
			s.fail(w, r, internal(e))
			return
		}
		s.write(w, r, 200, map[string]any{"provider_model": safeProviderModel(v)})
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		var m domain.ProviderModel
		if e := decodeJSON(w, r, &m); e != nil {
			s.fail(w, r, e)
			return
		}
		if id != "" {
			m.ID = id
		}
		if e := validateProviderModel(m); e != nil {
			s.fail(w, r, e)
			return
		}
		var e error
		if r.Method == http.MethodPost && id == "" {
			e = s.Repo.CreateProviderModel(ctx, m)
		} else {
			e = s.Repo.UpdateProviderModel(ctx, m)
		}
		if e != nil {
			s.fail(w, r, mapRepoError(e, "provider model"))
			return
		}
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"provider_model": safeProviderModel(m)})
	case http.MethodDelete:
		if id == "" {
			s.fail(w, r, badRequest("invalid_id", "provider model ID is required"))
			return
		}
		if e := s.Repo.DeleteProviderModel(ctx, id); e != nil {
			s.fail(w, r, mapRepoError(e, "provider model"))
			return
		}
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"deleted": id})
	default:
		s.fail(w, r, badRequest("method_not_allowed", "unsupported provider model method"))
	}
}
func (s *Server) groupResource(w http.ResponseWriter, r *http.Request, ctx context.Context, id string) {
	if id != "" && strings.HasSuffix(id, "/members") {
		id = strings.TrimSuffix(id, "/members")
		s.groupMembers(w, r, ctx, id)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if id == "" {
			v, e := s.Repo.ListModelGroups(ctx)
			if e != nil {
				s.fail(w, r, internal(e))
				return
			}
			s.write(w, r, 200, map[string]any{"model_groups": v})
			return
		}
		v, e := s.Repo.GetModelGroup(ctx, id)
		if errors.Is(e, repository.ErrNotFound) {
			s.fail(w, r, notFound("model group not found"))
			return
		}
		if e != nil {
			s.fail(w, r, internal(e))
			return
		}
		members, _ := s.Repo.ListModelGroupMembers(ctx, id)
		s.write(w, r, 200, map[string]any{"model_group": v, "members": members})
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		var g domain.ModelGroup
		if e := decodeJSON(w, r, &g); e != nil {
			s.fail(w, r, e)
			return
		}
		if id != "" {
			g.ID = id
		}
		if e := validateGroup(g); e != nil {
			s.fail(w, r, e)
			return
		}
		var e error
		if r.Method == http.MethodPost && id == "" {
			e = s.Repo.CreateModelGroup(ctx, g)
		} else {
			e = s.Repo.UpdateModelGroup(ctx, g)
		}
		if e != nil {
			s.fail(w, r, mapRepoError(e, "model group"))
			return
		}
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"model_group": g})
	case http.MethodDelete:
		if id == "" {
			s.fail(w, r, badRequest("invalid_id", "model group ID is required"))
			return
		}
		if e := s.Repo.DeleteModelGroup(ctx, id); e != nil {
			s.fail(w, r, mapRepoError(e, "model group"))
			return
		}
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"deleted": id})
	default:
		s.fail(w, r, badRequest("method_not_allowed", "unsupported model group method"))
	}
}
func (s *Server) groupMembers(w http.ResponseWriter, r *http.Request, ctx context.Context, id string) {
	switch r.Method {
	case http.MethodGet:
		v, e := s.Repo.ListModelGroupMembers(ctx, id)
		if e != nil {
			s.fail(w, r, internal(e))
			return
		}
		s.write(w, r, 200, map[string]any{"members": v})
	case http.MethodPost:
		var m domain.ModelGroupMember
		if e := decodeJSON(w, r, &m); e != nil {
			s.fail(w, r, e)
			return
		}
		m.GroupID = id
		if m.ModelID == "" {
			s.fail(w, r, badRequest("validation_error", "model_id is required"))
			return
		}
		if e := s.Repo.AddModelGroupMember(ctx, m); e != nil {
			s.fail(w, r, mapRepoError(e, "model group member"))
			return
		}
		s.notifyConfigChange(ctx)
		s.write(w, r, 201, map[string]any{"member": m})
	case http.MethodDelete:
		mid := r.URL.Query().Get("model_id")
		if mid == "" {
			s.fail(w, r, badRequest("invalid_id", "model_id is required"))
			return
		}
		if e := s.Repo.RemoveModelGroupMember(ctx, id, mid); e != nil {
			s.fail(w, r, mapRepoError(e, "model group member"))
			return
		}
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"deleted": mid})
	default:
		s.fail(w, r, badRequest("method_not_allowed", "unsupported member method"))
	}
}
func (s *Server) routeResource(w http.ResponseWriter, r *http.Request, ctx context.Context, id string) {
	switch r.Method {
	case http.MethodGet:
		if id == "" {
			v, e := s.Repo.ListRoutes(ctx)
			if e != nil {
				s.fail(w, r, internal(e))
				return
			}
			s.write(w, r, 200, map[string]any{"routes": v})
			return
		}
		v, e := s.Repo.GetRoute(ctx, id)
		if errors.Is(e, repository.ErrNotFound) {
			s.fail(w, r, notFound("route not found"))
			return
		}
		if e != nil {
			s.fail(w, r, internal(e))
			return
		}
		s.write(w, r, 200, map[string]any{"route": v})
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		var v domain.Route
		if e := decodeJSON(w, r, &v); e != nil {
			s.fail(w, r, e)
			return
		}
		if id != "" {
			v.ID = id
		}
		if e := validateRoute(v); e != nil {
			s.fail(w, r, e)
			return
		}
		var e error
		if r.Method == http.MethodPost && id == "" {
			e = s.Repo.CreateRoute(ctx, v)
		} else {
			e = s.Repo.UpdateRoute(ctx, v)
		}
		if e != nil {
			s.fail(w, r, mapRepoError(e, "route"))
			return
		}
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"route": v})
	case http.MethodDelete:
		if id == "" {
			s.fail(w, r, badRequest("invalid_id", "route ID is required"))
			return
		}
		if e := s.Repo.DeleteRoute(ctx, id); e != nil {
			s.fail(w, r, mapRepoError(e, "route"))
			return
		}
		s.notifyConfigChange(ctx)
		s.write(w, r, 200, map[string]any{"deleted": id})
	default:
		s.fail(w, r, badRequest("method_not_allowed", "unsupported route method"))
	}
}

func (s *Server) checkin(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method == http.MethodPost {
		s.runCheckin(w, r)
		return
	}
	if r.Method != http.MethodGet {
		s.fail(w, r, badRequest("method_not_allowed", "GET or POST required"))
		return
	}
	if s.Repo == nil {
		s.write(w, r, 200, map[string]any{"supported": false, "records": []any{}})
		return
	}
	channels, err := s.Repo.ListChannels(r.Context(), "")
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	records := []domain.CheckinRecord{}
	for _, ch := range channels {
		if id := r.URL.Query().Get("channel_id"); id != "" && id != ch.ID {
			continue
		}
		items, err := s.Repo.ListCheckinRecords(r.Context(), ch.ID)
		if err != nil {
			s.fail(w, r, internal(err))
			return
		}
		records = append(records, items...)
	}
	s.write(w, r, 200, map[string]any{"supported": s.Scheduler != nil, "records": records})
}
func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		s.fail(w, r, unsupported("log mutation is not supported"))
		return
	}
	if s.Repo == nil {
		s.write(w, r, 200, map[string]any{"supported": false, "entries": []any{}})
		return
	}
	records, err := s.Repo.ListRequestRecords(r.Context())
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	s.write(w, r, 200, map[string]any{
		"supported": true,
		"records":   records,
		"total":     len(records),
	})
}
func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		s.fail(w, r, unsupported("usage mutation is not supported"))
		return
	}
	if s.Repo == nil {
		s.write(w, r, 200, map[string]any{"supported": false, "records": []any{}})
		return
	}
	v, e := s.Repo.ListRequestRecords(r.Context())
	if e != nil {
		s.fail(w, r, internal(e))
		return
	}

	var totalTokens int64
	var totalLatency int64
	var successCount int
	modelStats := make(map[string]int)
	channelStats := make(map[string]int)

	for _, rec := range v {
		totalTokens += int64(rec.InputTokens + rec.OutputTokens)
		totalLatency += int64(rec.LatencyMS)
		if rec.StatusCode == http.StatusOK {
			successCount++
		}
		if rec.ModelID != "" {
			modelStats[rec.ModelID]++
		}
		if rec.ChannelID != "" {
			channelStats[rec.ChannelID]++
		}
	}

	avgLatency := int64(0)
	successRate := 100.0
	if len(v) > 0 {
		avgLatency = totalLatency / int64(len(v))
		successRate = float64(successCount) * 100.0 / float64(len(v))
	}

	s.write(w, r, 200, map[string]any{
		"supported": true,
		"records":   v,
		"summary": map[string]any{
			"total_requests": len(v),
			"success_count":  successCount,
			"success_rate":   fmt.Sprintf("%.1f%%", successRate),
			"total_tokens":   totalTokens,
			"avg_latency_ms": avgLatency,
			"model_stats":    modelStats,
			"channel_stats":  channelStats,
		},
	})
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		s.fail(w, r, unsupported("settings persistence is not configured"))
		return
	}
	bInfo := browser.Detect()
	s.write(w, r, 200, map[string]any{
		"local_only":          s.LocalOnly,
		"management_address":  s.Management,
		"token_configured":    s.Token != "",
		"supported_mutations": false,
		"browser":             bInfo,
	})
}

func (s *Server) browserHandler(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		s.fail(w, r, badRequest("method_not_allowed", "GET is required"))
		return
	}
	info := browser.Detect()
	s.write(w, r, http.StatusOK, info)
}

// cliSync exposes the CLI configuration sync lifecycle: GET reports detected
// targets and past attempts, POST runs preview / apply / verify. Every mutation
// goes through clisync.Service, which backs up, writes atomically, verifies the
// result and rolls back on failure — the UI must never be able to report a
// success that did not land on disk.
func (s *Server) cliSync(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.cliSyncStatus(w, r)
	case http.MethodPost:
		s.cliSyncMutate(w, r)
	default:
		s.fail(w, r, badRequest("method_not_allowed", "GET or POST is required"))
	}
}

func (s *Server) cliSyncStatus(w http.ResponseWriter, r *http.Request) {
	records := []domain.CLISyncRecord{}
	if s.Repo != nil {
		v, err := s.Repo.ListCLISyncRecords(r.Context())
		if err != nil {
			s.fail(w, r, internal(err))
			return
		}
		records = v
	}
	if s.CLISync == nil {
		s.write(w, r, http.StatusOK, map[string]any{
			"supported": false,
			"targets":   []any{},
			"records":   records,
			"message":   "CLI synchronization service is not configured",
		})
		return
	}
	targets, err := s.CLISync.Targets(r.Context())
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	s.write(w, r, http.StatusOK, map[string]any{
		"supported": true,
		"targets":   targets,
		"records":   records,
	})
}

func (s *Server) cliSyncMutate(w http.ResponseWriter, r *http.Request) {
	if s.CLISync == nil {
		s.fail(w, r, unsupported("CLI synchronization service is not configured; no mutation was performed"))
		return
	}
	var input struct {
		CLI     string `json:"cli"`
		Action  string `json:"action"`
		Desired struct {
			BaseURL  string `json:"base_url"`
			Model    string `json:"model"`
			Provider string `json:"provider"`
		} `json:"desired"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	if input.CLI == "" {
		s.fail(w, r, badRequest("missing_cli", "cli is required"))
		return
	}
	// The API never accepts a caller-supplied API key: keys are minted by the
	// local key service so the gateway will actually honour them.
	desired := clisync.DesiredState{
		BaseURL:  input.Desired.BaseURL,
		Model:    input.Desired.Model,
		Provider: input.Desired.Provider,
	}

	switch input.Action {
	case "preview":
		diff, err := s.CLISync.Preview(r.Context(), input.CLI, desired)
		if err != nil {
			s.fail(w, r, cliSyncFault(err))
			return
		}
		s.write(w, r, http.StatusOK, map[string]any{"action": "preview", "cli": input.CLI, "diff": diff})

	case "apply":
		result, err := s.CLISync.Apply(r.Context(), input.CLI, desired)
		if err != nil {
			s.fail(w, r, cliSyncFault(err))
			return
		}
		s.auditEvent(r.Context(), "cli_sync_apply", r, map[string]string{"cli": input.CLI, "config_path": result.Diff.Target.ConfigPath})
		s.write(w, r, http.StatusOK, map[string]any{
			"action": "apply",
			"cli":    result.CLI,
			"status": result.Status,
			"backup": result.Backup,
			"diff":   result.Diff,
		})

	case "verify":
		if err := s.CLISync.Verify(r.Context(), input.CLI, desired); err != nil {
			s.fail(w, r, cliSyncFault(err))
			return
		}
		s.write(w, r, http.StatusOK, map[string]any{"action": "verify", "cli": input.CLI, "status": "ok"})

	default:
		s.fail(w, r, badRequest("invalid_action", "action must be preview, apply or verify"))
	}
}

// cliSyncFault maps sync failures onto the API error envelope. An unknown CLI is
// a client mistake; everything else is reported as a server-side failure with
// the real cause, because a vague error here sends operators hunting blind.
func cliSyncFault(err error) error {
	if errors.Is(err, clisync.ErrUnknownCLI) {
		return badRequest("unknown_cli", err.Error())
	}
	return fault{status: http.StatusInternalServerError, code: "cli_sync_failed", message: err.Error(), cause: err}
}

// importExport exposes configuration portability: GET advertises support, POST
// runs export / preview / apply. Import is always a two-step flow — callers must
// preview before applying, and secrets only travel inside a password-encrypted
// section.
func (s *Server) importExport(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.write(w, r, http.StatusOK, map[string]any{
			"supported":       s.Repo != nil,
			"secrets_capable": s.SecretStore != nil,
			"actions":         []string{"export", "preview", "apply"},
		})
	case http.MethodPost:
		s.importExportMutate(w, r)
	default:
		s.fail(w, r, badRequest("method_not_allowed", "GET or POST is required"))
	}
}

func (s *Server) importExportMutate(w http.ResponseWriter, r *http.Request) {
	if s.Repo == nil {
		s.fail(w, r, unsupported("import/export service is not configured; no mutation was performed"))
		return
	}
	var input struct {
		Action         string `json:"action"`
		IncludeSecrets bool   `json:"include_secrets"`
		Password       string `json:"password"`
		Package        string `json:"package"`
		Policy         string `json:"policy"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}

	switch input.Action {
	case "export":
		if input.IncludeSecrets && input.Password == "" {
			s.fail(w, r, badRequest("password_required", "exporting secrets requires a password"))
			return
		}
		opts := export.ExportOptions{IncludeSecrets: input.IncludeSecrets, Password: input.Password}
		if input.IncludeSecrets {
			if s.SecretStore == nil {
				s.fail(w, r, unsupported("secret store is not configured; cannot export secrets"))
				return
			}
			store := s.SecretStore
			opts.SecretExporter = func(ctx context.Context) (map[string][]byte, error) {
				return store.ListAll(ctx)
			}
		}
		pkg, err := export.Create(r.Context(), s.Repo, opts)
		if err != nil {
			s.fail(w, r, internal(err))
			return
		}
		s.auditEvent(r.Context(), "config_export", r, map[string]string{"with_secrets": strconv.FormatBool(input.IncludeSecrets)})
		s.write(w, r, http.StatusOK, map[string]any{"action": "export", "package": string(pkg)})

	case "preview":
		if input.Package == "" {
			s.fail(w, r, badRequest("missing_package", "package is required"))
			return
		}
		preview, err := export.PreviewPackage(r.Context(), []byte(input.Package), s.Repo)
		if err != nil {
			s.fail(w, r, importFault(err))
			return
		}
		s.write(w, r, http.StatusOK, map[string]any{"action": "preview", "preview": preview})

	case "apply":
		store, ok := s.Repo.(*repository.Store)
		if !ok {
			s.fail(w, r, unsupported("configured repository does not support transactional import"))
			return
		}
		if input.Package == "" {
			s.fail(w, r, badRequest("missing_package", "package is required"))
			return
		}
		policy := export.ConflictSkip
		switch input.Policy {
		case "", string(export.ConflictSkip):
			policy = export.ConflictSkip
		case string(export.ConflictOverwrite):
			policy = export.ConflictOverwrite
		default:
			s.fail(w, r, badRequest("invalid_policy", "policy must be skip or overwrite"))
			return
		}
		opts := export.ApplyOptions{Policy: policy, Password: input.Password}
		if s.SecretStore != nil {
			secStore := s.SecretStore
			opts.SecretImporter = func(ctx context.Context, secretsMap map[string][]byte) error {
				for ref, value := range secretsMap {
					if err := secStore.Put(ctx, ref, value); err != nil {
						return err
					}
				}
				return nil
			}
		}
		if err := export.ApplyWithOptions(r.Context(), []byte(input.Package), *store, opts); err != nil {
			s.fail(w, r, importFault(err))
			return
		}
		if s.OnConfigChange != nil {
			if err := s.OnConfigChange(r.Context()); err != nil {
				s.fail(w, r, internal(err))
				return
			}
		}
		s.auditEvent(r.Context(), "config_import", r, map[string]string{"policy": string(policy)})
		s.write(w, r, http.StatusOK, map[string]any{"action": "apply", "status": "success", "policy": string(policy)})

	default:
		s.fail(w, r, badRequest("invalid_action", "action must be export, preview or apply"))
	}
}

// importFault distinguishes a bad package (the caller's problem) from an
// internal failure, so a tampered or wrongly-keyed file does not surface as a
// generic 500.
func importFault(err error) error {
	if errors.Is(err, export.ErrTamperedPackage) {
		return badRequest("tampered_package", "package integrity check failed")
	}
	return fault{status: http.StatusBadRequest, code: "import_failed", message: err.Error(), cause: err}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return badRequest("invalid_json", "request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return badRequest("invalid_json", "request body is required")
		}
		return badRequest("invalid_json", "malformed or unknown request fields")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return badRequest("invalid_json", "request body must contain one JSON value")
	}
	return nil
}
func requireID(kind, id string) error {
	if strings.TrimSpace(id) == "" {
		return badRequest("validation_error", kind+" id is required")
	}
	return nil
}
func validateProvider(p domain.Provider) error {
	if e := requireID("provider", p.ID); e != nil {
		return e
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Protocol) == "" {
		return badRequest("validation_error", "provider name and protocol are required")
	}
	if p.AdapterType == "" {
		p.AdapterType = "generic"
	}
	return nil
}
func validateChannel(c domain.Channel) error {
	if e := requireID("channel", c.ID); e != nil {
		return e
	}
	if strings.TrimSpace(c.ProviderID) == "" || strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return badRequest("validation_error", "channel provider_id, name, and base_url are required")
	}
	if c.Weight < 0 {
		return badRequest("validation_error", "channel weight must not be negative")
	}
	if c.CheckinMode != "" && c.CheckinMode != "auto" && c.CheckinMode != "manual" {
		return badRequest("validation_error", "channel checkin_mode must be 'auto' or 'manual'")
	}
	return nil
}
func validateModel(m domain.Model) error {
	if e := requireID("model", m.ID); e != nil {
		return e
	}
	if strings.TrimSpace(m.DisplayName) == "" {
		return badRequest("validation_error", "model display_name is required")
	}
	if m.ContextWindow < 0 || m.MaxOutputTokens < 0 {
		return badRequest("validation_error", "model limits must not be negative")
	}
	return nil
}
func validateProviderModel(m domain.ProviderModel) error {
	if e := requireID("provider model", m.ID); e != nil {
		return e
	}
	if m.ProviderID == "" || m.ModelID == "" || strings.TrimSpace(m.UpstreamModelName) == "" || strings.TrimSpace(m.Protocol) == "" {
		return badRequest("validation_error", "provider model provider_id, model_id, upstream_model_name, and protocol are required")
	}
	if m.Weight < 0 {
		return badRequest("validation_error", "provider model weight must not be negative")
	}
	return nil
}
func validateGroup(g domain.ModelGroup) error {
	if e := requireID("model group", g.ID); e != nil {
		return e
	}
	if strings.TrimSpace(g.Name) == "" || strings.TrimSpace(g.Strategy) == "" {
		return badRequest("validation_error", "model group name and strategy are required")
	}
	return nil
}
func validateRoute(v domain.Route) error {
	if e := requireID("route", v.ID); e != nil {
		return e
	}
	if strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.Protocol) == "" || strings.TrimSpace(v.Strategy) == "" {
		return badRequest("validation_error", "route name, protocol, and strategy are required")
	}
	if v.GroupID == "" && strings.TrimSpace(v.ModelPattern) == "" {
		return badRequest("validation_error", "route model_pattern or group_id is required")
	}
	return nil
}
func mapRepoError(err error, kind string) error {
	if errors.Is(err, repository.ErrNotFound) {
		return notFound(kind + " not found")
	}
	if errors.Is(err, repository.ErrProviderReferenced) {
		return conflict("provider has referenced channels")
	}
	if strings.Contains(strings.ToLower(err.Error()), "constraint") || strings.Contains(strings.ToLower(err.Error()), "unique") {
		return conflict(kind + " conflicts with an existing resource")
	}
	return internal(err)
}

// keys handles listing, issuing, and revoking local API keys.
// POST /api/v1/keys -> 201 { "key": "rh_...", "id": "rh_..." } (raw key returned only once)
// GET /api/v1/keys -> 200 { "keys": [ { "id": "rh_..." } ] } (IDs only, never secrets)
// DELETE /api/v1/keys/{id} -> 200 { "status": "revoked" }
func (s *Server) keys(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if s.LocalKeys == nil {
		s.fail(w, r, fault{status: http.StatusNotImplemented, code: "not_implemented", message: "local key service not configured"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/keys")
	id = strings.TrimPrefix(id, "/")

	switch r.Method {
	case http.MethodGet:
		if id != "" {
			s.fail(w, r, badRequest("invalid_request", "path must be /api/v1/keys"))
			return
		}
		ids, err := s.LocalKeys.List()
		if err != nil {
			s.fail(w, r, internal(err))
			return
		}
		type keySummary struct {
			ID string `json:"id"`
		}
		summaries := make([]keySummary, 0, len(ids))
		for _, keyID := range ids {
			summaries = append(summaries, keySummary{ID: keyID})
		}
		s.write(w, r, http.StatusOK, map[string]any{"keys": summaries})

	case http.MethodPost:
		if id != "" {
			s.fail(w, r, badRequest("invalid_request", "path must be /api/v1/keys"))
			return
		}
		rawKey, err := s.LocalKeys.Create()
		if err != nil {
			s.fail(w, r, internal(err))
			return
		}
		keyID := rawKey
		if len(rawKey) >= 11 {
			keyID = rawKey[:11]
		}
		s.auditEvent(r.Context(), "create_local_key", r, map[string]string{"id": keyID})
		s.write(w, r, http.StatusCreated, map[string]any{
			"key": rawKey,
			"id":  keyID,
		})

	case http.MethodDelete:
		if id == "" {
			s.fail(w, r, badRequest("missing_id", "key id is required"))
			return
		}
		s.LocalKeys.Revoke(id)
		s.auditEvent(r.Context(), "revoke_local_key", r, map[string]string{"id": id})
		s.write(w, r, http.StatusOK, map[string]any{"status": "revoked", "id": id})

	default:
		s.fail(w, r, badRequest("method_not_allowed", "method not supported"))
	}
}

// Secret-bearing references are safe to return; secret values are never part
// of domain objects. CredentialRef is deliberately retained as a reference.
func safeProvider(p domain.Provider) domain.Provider                { return p }
func safeProviders(v []domain.Provider) []domain.Provider           { return v }
func safeChannel(c domain.Channel) domain.Channel                   { return c }
func safeChannels(v []domain.Channel) []domain.Channel              { return v }
func safeProviderModel(v domain.ProviderModel) domain.ProviderModel { return v }

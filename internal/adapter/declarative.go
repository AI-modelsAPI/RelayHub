package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/keybind"
)

type DeclarativeStep struct {
	Method  string            `json:"method" yaml:"method"`
	Path    string            `json:"path" yaml:"path"`
	Headers map[string]string `json:"headers" yaml:"headers"`
	Body    string            `json:"body" yaml:"body"`
}

type DeclarativeConfig struct {
	Name    string          `json:"name" yaml:"name"`
	CheckIn DeclarativeStep `json:"checkin" yaml:"checkin"`
	Balance DeclarativeStep `json:"balance" yaml:"balance"`
	Timeout time.Duration   `json:"timeout" yaml:"timeout"`
}

type DeclarativeAdapter struct {
	Config  DeclarativeConfig
	Client  *http.Client
	Secrets SecretResolver
	Clients ClientProvider
}

// SetClientProvider implements EgressAware.
func (d *DeclarativeAdapter) SetClientProvider(p ClientProvider) { d.Clients = p }

// httpClient mirrors BaseNewAPIAdapter.HTTPClient: wired egress first, then
// the channel's own proxy (never silently the default egress, AUDIT RH-10),
// then the adapter client.
func (d *DeclarativeAdapter) httpClient(ch domain.Channel) *http.Client {
	timeout := d.Config.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if d.Clients != nil {
		if c := d.Clients.ClientFor(ch, timeout); c != nil {
			return c
		}
	}
	if strings.TrimSpace(ch.ProxyURL) != "" {
		return defaultEgress.ClientFor(ch, timeout)
	}
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: timeout}
}

func NewDeclarativeAdapter(cfg DeclarativeConfig, client *http.Client) (*DeclarativeAdapter, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("%w: declarative adapter name is required", ErrInvalidConfig)
	}
	if err := validateStep(cfg.CheckIn); err != nil {
		return nil, fmt.Errorf("invalid checkin step: %w", err)
	}
	if err := validateStep(cfg.Balance); err != nil {
		return nil, fmt.Errorf("invalid balance step: %w", err)
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &DeclarativeAdapter{
		Config: cfg,
		Client: client,
	}, nil
}

func validateStep(s DeclarativeStep) error {
	if s.Method == "" && s.Path == "" {
		return nil // optional step
	}
	m := strings.ToUpper(strings.TrimSpace(s.Method))
	if m != http.MethodGet && m != http.MethodPost {
		return fmt.Errorf("%w: unsupported method %q (only GET/POST allowed)", ErrSecurityViolation, s.Method)
	}
	// Path must be clean and not escape root
	cleaned := path.Clean("/" + s.Path)
	if strings.Contains(s.Path, "..") || strings.HasPrefix(s.Path, "http://") || strings.HasPrefix(s.Path, "https://") {
		return fmt.Errorf("%w: path traversal or absolute url forbidden: %s", ErrSecurityViolation, s.Path)
	}
	if cleaned == "/" && s.Path != "" && s.Path != "/" {
		return fmt.Errorf("%w: invalid path %s", ErrSecurityViolation, s.Path)
	}
	return nil
}

func (d *DeclarativeAdapter) Validate(ctx context.Context, channel domain.Channel) error {
	if strings.TrimSpace(channel.BaseURL) == "" {
		return fmt.Errorf("%w: channel base_url is required", ErrInvalidConfig)
	}
	u, err := url.Parse(channel.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%w: base_url must be http/https: %s", ErrInvalidConfig, channel.BaseURL)
	}
	return nil
}

func (d *DeclarativeAdapter) Refresh(ctx context.Context, channel domain.Channel) error {
	return nil
}

func (d *DeclarativeAdapter) CheckIn(ctx context.Context, channel domain.Channel) (CheckInResult, error) {
	if d.Config.CheckIn.Method == "" {
		return CheckInResult{}, fmt.Errorf("%w: checkin not configured", ErrUnsupportedOperation)
	}
	respBody, err := d.executeStep(ctx, channel, d.Config.CheckIn)
	if err != nil {
		return CheckInResult{Success: false, Message: err.Error()}, err
	}
	// Try parsing standard JSON success/message. Anything that does not carry
	// an explicit success:true is NOT a success: unparseable check-in responses
	// previously fabricated success:true, masking every failure (AUDIT RH-26).
	var result struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Reward  string `json:"reward"`
	}
	if err := json.Unmarshal(respBody, &result); err == nil && result.Success {
		return CheckInResult{
			Success: true,
			Reward:  result.Reward,
			Message: result.Message,
		}, nil
	}
	msg := string(respBody)
	if len(msg) > 256 {
		msg = msg[:256]
	}
	return CheckInResult{Success: false, Message: "unrecognized check-in response: " + msg}, nil
}

func (d *DeclarativeAdapter) Balance(ctx context.Context, channel domain.Channel) (BalanceResult, error) {
	if d.Config.Balance.Method == "" {
		return BalanceResult{}, fmt.Errorf("%w: balance not configured", ErrUnsupportedOperation)
	}
	respBody, err := d.executeStep(ctx, channel, d.Config.Balance)
	if err != nil {
		return BalanceResult{}, err
	}
	var res struct {
		Quota     int64 `json:"quota"`
		Remaining int64 `json:"remaining"`
		Total     int64 `json:"total"`
	}
	_ = json.Unmarshal(respBody, &res)
	rem := res.Remaining
	if rem == 0 && res.Quota > 0 {
		rem = res.Quota
	}
	return BalanceResult{Remaining: rem, Total: res.Total}, nil
}

func (d *DeclarativeAdapter) Models(ctx context.Context, channel domain.Channel) ([]ModelInfo, error) {
	return nil, ErrUnsupportedOperation
}

func (d *DeclarativeAdapter) Health(ctx context.Context, channel domain.Channel) (HealthResult, error) {
	return HealthResult{Status: "healthy"}, nil
}

func (d *DeclarativeAdapter) executeStep(ctx context.Context, channel domain.Channel, step DeclarativeStep) ([]byte, error) {
	base, err := url.Parse(channel.BaseURL)
	if err != nil {
		return nil, err
	}
	targetURL := base.ResolveReference(&url.URL{Path: path.Clean("/" + step.Path)})

	var bodyReader io.Reader
	if step.Body != "" {
		bodyReader = bytes.NewBufferString(step.Body)
	}

	req, err := http.NewRequestWithContext(ctx, step.Method, targetURL.String(), bodyReader)
	if err != nil {
		return nil, err
	}

	for k, v := range step.Headers {
		req.Header.Set(k, v)
	}
	if channel.CredentialRef != "" && req.Header.Get("Authorization") == "" && d.Secrets != nil && keybind.Allows(channel.CredentialRef, channel.ID, channel.BaseURL) {
		// Resolve the ref to an actual credential; without a resolver the ref
		// is a locator, not a token (AUDIT RH-26).
		if token, terr := d.Secrets.Get(ctx, channel.CredentialRef); terr == nil && len(token) > 0 {
			req.Header.Set("Authorization", "Bearer "+string(token))
		}
	}

	resp, err := d.httpClient(channel).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("step %s %s failed with status: %d", step.Method, step.Path, resp.StatusCode)
	}

	// Limit response reading to 2MB
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

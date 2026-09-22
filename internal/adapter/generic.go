package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"relayhub/internal/domain"
)

// GenericAdapter handles standard OpenAI/Anthropic/Gemini compatible endpoints.
// It explicitly refuses check-in operations.
type GenericAdapter struct {
	Client  *http.Client
	Secrets SecretResolver
}

// NewGenericAdapter keeps the original signature for existing callers (tests).
// Production wiring must use NewGenericAdapterWithSecrets so CredentialRef
// values resolve via the secret store instead of being sent as bearer tokens
// (AUDIT RH-26).
func NewGenericAdapter(client *http.Client) *GenericAdapter {
	return NewGenericAdapterWithSecrets(client, nil)
}

func NewGenericAdapterWithSecrets(client *http.Client, secrets SecretResolver) *GenericAdapter {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &GenericAdapter{Client: client, Secrets: secrets}
}

// bearerToken resolves the channel credential; without a secret resolver the
// ref is a locator, not a token, so the adapter must not send it.
func (g *GenericAdapter) bearerToken(ctx context.Context, channel domain.Channel) string {
	if channel.CredentialRef == "" || g.Secrets == nil {
		return ""
	}
	secret, err := g.Secrets.Get(ctx, channel.CredentialRef)
	if err != nil {
		return ""
	}
	return string(secret)
}

func (g *GenericAdapter) Validate(ctx context.Context, channel domain.Channel) error {
	if strings.TrimSpace(channel.BaseURL) == "" {
		return fmt.Errorf("%w: channel base_url is required", ErrInvalidConfig)
	}
	return nil
}

func (g *GenericAdapter) Refresh(ctx context.Context, channel domain.Channel) error {
	// Generic API keys do not need automatic refresh
	return nil
}

func (g *GenericAdapter) CheckIn(ctx context.Context, channel domain.Channel) (CheckInResult, error) {
	// Generic adapters NEVER infer or perform check-in
	return CheckInResult{Success: false, Message: "check-in is not supported on generic providers"}, ErrUnsupportedOperation
}

func (g *GenericAdapter) Balance(ctx context.Context, channel domain.Channel) (BalanceResult, error) {
	return BalanceResult{}, ErrUnsupportedOperation
}

func (g *GenericAdapter) Models(ctx context.Context, channel domain.Channel) ([]ModelInfo, error) {
	baseURL := strings.TrimRight(channel.BaseURL, "/")
	reqURL := baseURL + "/models"
	if !strings.HasSuffix(baseURL, "/v1") {
		reqURL = baseURL + "/v1/models"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	if token := g.bearerToken(ctx, channel); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Honour the channel proxy instead of always using the process-wide
	// client (AUDIT RH-10).
	resp, err := clientForChannel(g.Client, channel).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models request failed with status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	var result []ModelInfo
	for _, m := range data.Data {
		result = append(result, ModelInfo{
			ID:          m.ID,
			DisplayName: m.ID,
		})
	}
	return result, nil
}

func (g *GenericAdapter) Health(ctx context.Context, channel domain.Channel) (HealthResult, error) {
	start := time.Now()
	models, err := g.Models(ctx, channel)
	latency := time.Since(start)
	if err != nil {
		return HealthResult{
			Status:  "degraded",
			Latency: latency,
			Message: err.Error(),
		}, nil
	}
	return HealthResult{
		Status:  "healthy",
		Latency: latency,
		Message: fmt.Sprintf("%d models discovered", len(models)),
	}, nil
}

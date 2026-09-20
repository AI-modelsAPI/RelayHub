package adapter

import (
	"context"
	"errors"
	"time"

	"relayhub/internal/domain"
)

var (
	ErrUnsupportedOperation = errors.New("unsupported operation for adapter")
	ErrAdapterNotFound      = errors.New("adapter not found")
	ErrInvalidConfig        = errors.New("invalid adapter configuration")
	ErrSecurityViolation    = errors.New("security policy violation")
	ErrSecretRequired       = errors.New("secret store or secret resolution required")
	ErrNeedWebview          = errors.New("turnstile or human verification required; offscreen webview required")
	ErrNeedsRelogin         = errors.New("account requires relogin to trigger daily reward")
	ErrCheckinDisabled      = errors.New("checkin is disabled for this adapter")
)

type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type CheckInResult struct {
	Success      bool    `json:"success"`
	Already      bool    `json:"already,omitempty"`
	Reward       string  `json:"reward,omitempty"`
	RewardUSD    float64 `json:"reward_usd,omitempty"`
	RewardKnown  bool    `json:"reward_known,omitempty"`
	Message      string  `json:"message"`
	NeedWebview  bool    `json:"need_webview,omitempty"`
	NeedsRelogin bool    `json:"needs_relogin,omitempty"`
}

type BalanceResult struct {
	Remaining    int64     `json:"remaining"`
	Total        int64     `json:"total"`
	ResetAt      time.Time `json:"reset_at"`
	AvailableUSD float64   `json:"available_usd,omitempty"`
	UsedUSD      float64   `json:"used_usd,omitempty"`
	TodayUsedUSD float64   `json:"today_used_usd,omitempty"`
	QuotaPerUnit int64     `json:"quota_per_unit,omitempty"`
	Username     string    `json:"username,omitempty"`
}

type HealthResult struct {
	Status  string        `json:"status"`
	Latency time.Duration `json:"latency"`
	Message string        `json:"message,omitempty"`
}

// SecretResolver abstracts secrets.Store lookup without forcing tight coupling.
type SecretResolver interface {
	Get(ctx context.Context, ref string) ([]byte, error)
	Put(ctx context.Context, ref string, plaintext []byte) error
}

// ProviderAdapter defines the common operations supported by upstream adapters.
type ProviderAdapter interface {
	Validate(ctx context.Context, channel domain.Channel) error
	Refresh(ctx context.Context, channel domain.Channel) error
	CheckIn(ctx context.Context, channel domain.Channel) (CheckInResult, error)
	Balance(ctx context.Context, channel domain.Channel) (BalanceResult, error)
	Models(ctx context.Context, channel domain.Channel) ([]ModelInfo, error)
	Health(ctx context.Context, channel domain.Channel) (HealthResult, error)
}

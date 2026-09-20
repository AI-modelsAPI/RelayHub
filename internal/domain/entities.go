package domain

import "time"

type Provider struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	AdapterType     string    `json:"adapter_type"`
	Protocol        string    `json:"protocol"`
	BaseURLTemplate string    `json:"base_url_template"`
	Capabilities    string    `json:"capabilities"`
	CheckinEnabled  bool      `json:"checkin_enabled"`
	AdapterVersion  string    `json:"adapter_version"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type Channel struct {
	ID             string            `json:"id"`
	ProviderID     string            `json:"provider_id"`
	Name           string            `json:"name"`
	BaseURL        string            `json:"base_url"`
	CredentialRef  string            `json:"credential_ref"`
	AccountRef     string            `json:"account_ref"`
	CustomHeaders  map[string]string `json:"custom_headers,omitempty"`
	RoutingTags    string            `json:"routing_tags"`
	QuotaState     string            `json:"quota_state"`
	HealthState    string            `json:"health_state"`
	RateLimitState string            `json:"rate_limit_state"`
	Priority       int               `json:"priority"`
	Weight         int               `json:"weight"`
	CheckinEnabled bool              `json:"checkin_enabled"`
	RoutingEnabled bool              `json:"routing_enabled"`
	Enabled        bool              `json:"enabled"`
	// AxonHub-compatible extensions
	Status           string    `json:"status"`             // enabled | disabled | archived
	ManualModels     string    `json:"manual_models"`      // comma-separated manual model list
	AutoSync         bool      `json:"auto_sync"`          // auto-sync supported models
	AutoSyncPattern  string    `json:"auto_sync_pattern"`  // regex filter for auto-sync
	DefaultTestModel string    `json:"default_test_model"` // model used for connectivity test
	StreamPolicy     string    `json:"stream_policy"`      // unlimited | disabled
	ErrorMessage     string    `json:"error_message"`      // last upstream error
	Remark           string    `json:"remark"`             // user remark
	ProxyURL         string    `json:"proxy_url"`          // upstream proxy (socks5://...)
	CheckinMode      string    `json:"checkin_mode"`       // auto | manual
	CreatedAt        time.Time `json:"created_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type Credential struct {
	ID         string    `json:"id"`
	ChannelID  string    `json:"channel_id"`
	Kind       string    `json:"kind"`
	Ciphertext []byte    `json:"-"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

// ChannelKey is one of possibly many API keys bound to a channel. The secret
// value itself lives in the encrypted secret store under SecretRef; this record
// only carries metadata so the UI can list/enable/disable/delete keys without
// ever exposing plaintext. Disabled keys are skipped by the gateway rotation.
type ChannelKey struct {
	ID        string    `json:"id"`
	ChannelID string    `json:"channel_id"`
	SecretRef string    `json:"secret_ref"`
	Label     string    `json:"label"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type Model struct {
	ID                   string   `json:"id"`
	DisplayName          string   `json:"display_name"`
	Aliases              []string `json:"aliases"`
	Family               string   `json:"family"`
	ProtocolRequirements string   `json:"protocol_requirements"`
	Capabilities         string   `json:"capabilities"`
	ContextWindow        int      `json:"context_window"`
	MaxOutputTokens      int      `json:"max_output_tokens"`
	ReasoningSupport     bool     `json:"reasoning_support"`
	VisionSupport        bool     `json:"vision_support"`
	ToolCallSupport      bool     `json:"tool_call_support"`
	Enabled              bool     `json:"enabled"`
	// AxonHub-compatible model catalog extensions
	Developer   string  `json:"developer"`    // vendor/developer, e.g. OpenAI, Anthropic
	ModelType   string  `json:"model_type"`   // llm | embedding | image | rerank | audio
	InputPrice  float64 `json:"input_price"`  // price per 1M input tokens (USD), 0 = unset
	OutputPrice float64 `json:"output_price"` // price per 1M output tokens (USD), 0 = unset
	IconURL     string  `json:"icon_url"`     // optional model icon (URL or data URI)
	Archived    bool    `json:"archived"`     // archived models are hidden from active use

	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type ProviderModel struct {
	ID                string    `json:"id"`
	ProviderID        string    `json:"provider_id"`
	ChannelID         string    `json:"channel_id,omitempty"`
	ModelID           string    `json:"model_id"`
	UpstreamModelName string    `json:"upstream_model_name"`
	Protocol          string    `json:"protocol"`
	RequestTransform  string    `json:"request_transform"`
	ResponseTransform string    `json:"response_transform"`
	Priority          int       `json:"priority"`
	Weight            int       `json:"weight"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type ModelGroup struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Strategy        string    `json:"strategy"`
	FallbackGroupID string    `json:"fallback_group_id,omitempty"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type ModelGroupMember struct {
	GroupID  string `json:"group_id"`
	ModelID  string `json:"model_id"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
}

type Route struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Protocol       string    `json:"protocol"`
	ModelPattern   string    `json:"model_pattern"`
	GroupID        string    `json:"group_id,omitempty"`
	ChannelFilter  string    `json:"channel_filter"`
	Strategy       string    `json:"strategy"`
	RetryPolicy    string    `json:"retry_policy"`
	FallbackPolicy string    `json:"fallback_policy"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

type CheckinRecord struct {
	ID           string     `json:"id"`
	ChannelID    string     `json:"channel_id"`
	Status       string     `json:"status"`
	Reward       string     `json:"reward"`
	ErrorCode    string     `json:"error_code"`
	ErrorMessage string     `json:"error_message"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

type HealthRecord struct {
	ID           string    `json:"id"`
	ChannelID    string    `json:"channel_id"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message"`
	CheckedAt    time.Time `json:"checked_at"`
	LatencyMS    int       `json:"latency_ms"`
}

type RequestRecord struct {
	ID               string    `json:"id"`
	RequestID        string    `json:"request_id"`
	Protocol         string    `json:"protocol"`
	ModelID          string    `json:"model_id,omitempty"`
	ProviderID       string    `json:"provider_id,omitempty"`
	ChannelID        string    `json:"channel_id,omitempty"`
	ErrorClass       string    `json:"error_class"`
	StatusCode       int       `json:"status_code"`
	LatencyMS        int       `json:"latency_ms"`
	TTFTMS           int       `json:"ttft_ms"`
	InputTokens      int       `json:"input_tokens"`
	OutputTokens     int       `json:"output_tokens"`
	CacheReadTokens  int       `json:"cache_read_tokens"`
	CacheWriteTokens int       `json:"cache_write_tokens"`
	FinishReason     string    `json:"finish_reason,omitempty"`
	UpstreamModel    string    `json:"upstream_model,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type CLISyncRecord struct {
	ID         string    `json:"id"`
	CLI        string    `json:"cli"`
	ConfigPath string    `json:"config_path"`
	Profile    string    `json:"profile"`
	BackupPath string    `json:"backup_path"`
	Status     string    `json:"status"`
	Error      string    `json:"error"`
	CreatedAt  time.Time `json:"created_at"`
}

type ConfigBackup struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultHTTPProxyAddr  = "127.0.0.1:8787"
	DefaultSOCKS5Addr     = "127.0.0.1:8788"
	DefaultGatewayAddr    = "127.0.0.1:8789"
	DefaultManagementAddr = "127.0.0.1:8790"
)

type Config struct {
	DataDir        string `json:"data_dir"`
	DatabasePath   string `json:"database_path"`
	BackupDir      string `json:"backup_dir"`
	ExportDir      string `json:"export_dir"`
	LogsDir        string `json:"logs_dir"`
	RuntimeDir     string `json:"runtime_dir"`
	HTTPProxyAddr  string `json:"http_proxy_addr"`
	SOCKS5Addr     string `json:"socks5_addr"`
	GatewayAddr    string `json:"gateway_addr"`
	ManagementAddr string `json:"management_addr"`
	// Proxy target policies: "open" (default) allows local, private and public
	// targets; "public_private" drops loopback/link-local; "public_only" also
	// drops RFC1918 ranges; "local_only" limits targets to loopback/link-local.
	// Every policy refuses RelayHub's own listener ports and cloud metadata
	// addresses (AUDIT 2026-09-24 F1-F3).
	HTTPProxyTargetPolicy string `json:"http_proxy_target_policy"`
	SOCKS5TargetPolicy    string `json:"socks5_target_policy"`
	// ProxyUsername / ProxyPassword, when both set, require credentials on
	// the local HTTP (Proxy-Authorization: Basic) and SOCKS5 (RFC 1929)
	// proxies, so other local processes and users cannot borrow the egress
	// (AUDIT 2026-09-24 F2). Env: RELAYHUB_PROXY_USERNAME / _PASSWORD.
	ProxyUsername string `json:"proxy_username"`
	ProxyPassword string `json:"proxy_password"`
	// MasterKeyStore selects where the secret-store key lives: "file"
	// (default, <data-dir>/master.key) or "keychain" (macOS login keychain;
	// an existing master.key is migrated and removed). AUDIT 2026-09-24 F18.
	// Env: RELAYHUB_MASTER_KEY_STORE.
	MasterKeyStore string `json:"master_key_store"`
	// EgressProxyURL is the global default exit for outbound AI gateway,
	// check-in and browser traffic (http://, https://, socks5://, or
	// "direct"). A channel's own proxy_url takes precedence. Empty means the
	// process environment (HTTPS_PROXY etc.) decides. Env: RELAYHUB_EGRESS_PROXY.
	EgressProxyURL string `json:"egress_proxy_url"`
	// ManagementToken, when set, is the bearer token ("Authorization: Bearer
	// …") the management API requires; otherwise a random token is generated
	// into <data-dir>/management.token (see ManagementAuth). Before this field
	// existed the server had no way to enable its token check (AUDIT
	// 2026-09-24 F4).
	ManagementToken string `json:"management_token"`
	// ManagementAuth controls management API authentication: "token"
	// (default) requires a bearer token on every /api/ call except liveness
	// and pairing — management_token when set, otherwise a random token kept
	// in <data-dir>/management.token; "off" restores the unauthenticated
	// loopback API (AUDIT 2026-09-24 F4). Env: RELAYHUB_MANAGEMENT_AUTH.
	ManagementAuth string `json:"management_auth"`
	// Notify configures outbound notifications (check-in failures, quota
	// low, breaker open). All fields optional; env overrides see ApplyEnv.
	Notify NotifyConfig `json:"notify"`
	// VerifyProbeInterval is how often authenticity probes (AUDIT §5 B1)
	// canary every routable channel: a Go duration of at least 10m, "off"
	// or "0" to disable, empty for DefaultVerifyProbeInterval.
	// Env: RELAYHUB_VERIFY_PROBE_INTERVAL.
	VerifyProbeInterval string `json:"verify_probe_interval"`
	// HealthProbeInterval is how often a tripped channel is probed back with
	// one cheap request (AUDIT §5 B5): a Go duration of at least 100ms, "off"
	// or "0" to disable, empty for DefaultHealthProbeInterval.
	// Env: RELAYHUB_HEALTH_PROBE_INTERVAL.
	HealthProbeInterval string `json:"health_probe_interval"`
	// StreamCommitWindow is how long a 2xx stream may be held back waiting
	// for its first output event before it is committed to the client (AUDIT
	// §5 B4): a Go duration of at least 100ms, "off" or "0" to forward the
	// upstream bytes immediately, empty for DefaultStreamCommitWindow.
	// Env: RELAYHUB_STREAM_COMMIT_WINDOW.
	StreamCommitWindow string `json:"stream_commit_window"`
	// BillingReconcileInterval is how often RelayHub compares its own token
	// ledger with the site's consumption log (AUDIT §5 B2): a Go duration of
	// at least 1m, "off" or "0" to disable the pass, empty for
	// DefaultBillingReconcileInterval. Env:
	// RELAYHUB_BILLING_RECONCILE_INTERVAL.
	BillingReconcileInterval string `json:"billing_reconcile_interval"`
}

// DefaultHealthProbeInterval applies when health_probe_interval is unset.
const DefaultHealthProbeInterval = 30 * time.Second

// DefaultStreamCommitWindow applies when stream_commit_window is unset.
const DefaultStreamCommitWindow = 10 * time.Second

// HealthProbe parses HealthProbeInterval; 0 means "keep the pre-B5 breaker
// behaviour", empty means DefaultHealthProbeInterval.
func (c Config) HealthProbe() (time.Duration, error) {
	v := strings.ToLower(strings.TrimSpace(c.HealthProbeInterval))
	switch v {
	case "":
		return DefaultHealthProbeInterval, nil
	case "0", "off", "false", "disabled":
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid health_probe_interval %q: %v", c.HealthProbeInterval, err)
	}
	if d < minHealthProbeInterval {
		return 0, fmt.Errorf("health_probe_interval %q is below the %s minimum (use \"off\" to disable)", c.HealthProbeInterval, minHealthProbeInterval)
	}
	return d, nil
}

// StreamCommit parses StreamCommitWindow; disabled means the upstream's first
// byte is forwarded immediately (no failover after a stream starts).
func (c Config) StreamCommit() (window time.Duration, disabled bool, err error) {
	v := strings.ToLower(strings.TrimSpace(c.StreamCommitWindow))
	switch v {
	case "":
		return DefaultStreamCommitWindow, false, nil
	case "0", "off", "false", "disabled":
		return 0, true, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, false, fmt.Errorf("invalid stream_commit_window %q: %v", c.StreamCommitWindow, err)
	}
	if d < minStreamCommitWindow {
		return 0, false, fmt.Errorf("stream_commit_window %q is below the %s minimum (use \"off\" to disable)", c.StreamCommitWindow, minStreamCommitWindow)
	}
	return d, false, nil
}

// Minimums keep a typo from disabling either safety feature by accident.
const (
	minHealthProbeInterval = 100 * time.Millisecond
	minStreamCommitWindow  = 100 * time.Millisecond
)

// DefaultBillingReconcileInterval applies when billing_reconcile_interval is
// unset: hourly is often enough to catch a rate change the same day without
// hammering the site's log endpoint.
const DefaultBillingReconcileInterval = time.Hour

// minBillingReconcileInterval keeps a typo from turning the reconciler into a
// poll loop against the relay.
const minBillingReconcileInterval = time.Minute

// BillingReconcile parses BillingReconcileInterval: empty means the default,
// "off"/"0" disables reconciliation, anything else must be a Go duration of at
// least 1m. Invalid values fail startup instead of silently changing cadence.
func (c Config) BillingReconcile() (time.Duration, error) {
	v := strings.ToLower(strings.TrimSpace(c.BillingReconcileInterval))
	switch v {
	case "":
		return DefaultBillingReconcileInterval, nil
	case "0", "off", "false", "disabled":
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid billing_reconcile_interval %q: %v", c.BillingReconcileInterval, err)
	}
	if d < minBillingReconcileInterval {
		return 0, fmt.Errorf("billing_reconcile_interval %q is below the %s minimum (use \"off\" to disable)", c.BillingReconcileInterval, minBillingReconcileInterval)
	}
	return d, nil
}

// DefaultVerifyProbeInterval applies when verify_probe_interval is unset.
const DefaultVerifyProbeInterval = 12 * time.Hour

// minVerifyProbeInterval keeps probes from hammering relays.
const minVerifyProbeInterval = 10 * time.Minute

// ProbeInterval parses VerifyProbeInterval: empty means the default, "off"
// or "0" disables periodic probes, anything else must be a Go duration of at
// least 10m. Invalid values are an error so a typo fails startup instead of
// silently changing the schedule.
func (c Config) ProbeInterval() (time.Duration, error) {
	v := strings.ToLower(strings.TrimSpace(c.VerifyProbeInterval))
	switch v {
	case "":
		return DefaultVerifyProbeInterval, nil
	case "0", "off", "false", "disabled":
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid verify_probe_interval %q: %v", c.VerifyProbeInterval, err)
	}
	if d < minVerifyProbeInterval {
		return 0, fmt.Errorf("verify_probe_interval %q is below the %s minimum (use \"off\" to disable)", c.VerifyProbeInterval, minVerifyProbeInterval)
	}
	return d, nil
}

// NotifyConfig holds notification sinks. Values are non-secret endpoints or
// bot tokens the user chose to put in the config file; the file lives in the
// data directory and should be mode 0600.
type NotifyConfig struct {
	WebhookURL       string  `json:"webhook_url"`
	BarkURL          string  `json:"bark_url"`
	TelegramBotToken string  `json:"telegram_bot_token"`
	TelegramChatID   string  `json:"telegram_chat_id"`
	QuotaLowUSD      float64 `json:"quota_low_usd"`
}

// ConfigFileName is the optional JSON file looked up inside the data directory.
const ConfigFileName = "config.json"

// ApplyEnv overlays environment variables onto cfg. It is applied after the
// file so that deployment environments can override without editing files.
func ApplyEnv(cfg *Config) {
	set := func(dst *string, key string) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			*dst = v
		}
	}
	set(&cfg.EgressProxyURL, "RELAYHUB_EGRESS_PROXY")
	set(&cfg.ManagementToken, "RELAYHUB_MANAGEMENT_TOKEN")
	set(&cfg.ManagementAuth, "RELAYHUB_MANAGEMENT_AUTH")
	set(&cfg.ProxyUsername, "RELAYHUB_PROXY_USERNAME")
	set(&cfg.ProxyPassword, "RELAYHUB_PROXY_PASSWORD")
	set(&cfg.MasterKeyStore, "RELAYHUB_MASTER_KEY_STORE")
	set(&cfg.Notify.WebhookURL, "RELAYHUB_NOTIFY_WEBHOOK_URL")
	set(&cfg.Notify.BarkURL, "RELAYHUB_NOTIFY_BARK_URL")
	set(&cfg.Notify.TelegramBotToken, "RELAYHUB_NOTIFY_TELEGRAM_TOKEN")
	set(&cfg.Notify.TelegramChatID, "RELAYHUB_NOTIFY_TELEGRAM_CHAT_ID")
	set(&cfg.VerifyProbeInterval, "RELAYHUB_VERIFY_PROBE_INTERVAL")
	set(&cfg.HealthProbeInterval, "RELAYHUB_HEALTH_PROBE_INTERVAL")
	set(&cfg.StreamCommitWindow, "RELAYHUB_STREAM_COMMIT_WINDOW")
	set(&cfg.BillingReconcileInterval, "RELAYHUB_BILLING_RECONCILE_INTERVAL")
	if v, ok := os.LookupEnv("RELAYHUB_NOTIFY_QUOTA_LOW_USD"); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Notify.QuotaLowUSD = f
		}
	}
}

func Defaults(dataDir string) Config {
	dataDir = normalizePath(dataDir, "")
	return Config{
		DataDir: dataDir, DatabasePath: filepath.Join(dataDir, "relayhub.db"),
		BackupDir: filepath.Join(dataDir, "backups"), ExportDir: filepath.Join(dataDir, "exports"),
		LogsDir: filepath.Join(dataDir, "logs"), RuntimeDir: filepath.Join(dataDir, "runtime"),
		HTTPProxyAddr: DefaultHTTPProxyAddr, SOCKS5Addr: DefaultSOCKS5Addr,
		GatewayAddr: DefaultGatewayAddr, ManagementAddr: DefaultManagementAddr,
		HTTPProxyTargetPolicy: "open", SOCKS5TargetPolicy: "open",
		ManagementAuth: "token",
	}
}

// Load resolves a data directory or an existing JSON configuration file. It
// deliberately accepts only non-secret settings; credentials belong in the
// secret store and are never read from this configuration.
func Load(path string) (Config, error) {
	if path == "" {
		path = defaultDataDir()
	}
	path = normalizePath(path, "")
	dataDir := path
	var file string
	if st, err := os.Stat(path); err == nil {
		if st.IsDir() {
			dataDir = path
			// A data directory may carry an optional config.json.
			if candidate := filepath.Join(path, ConfigFileName); fileExists(candidate) {
				file = candidate
			}
		} else {
			file, dataDir = path, filepath.Dir(path)
		}
	} else if !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("inspect config path: %w", err)
	} else if ext := filepath.Ext(path); ext == ".json" {
		file, dataDir = path, filepath.Dir(path)
	}
	dataDir = normalizePath(dataDir, filepath.Dir(file))
	cfg := Defaults(dataDir)
	if file == "" {
		return cfg, nil
	}
	contents, err := os.ReadFile(file)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var loaded Config
	if err := json.Unmarshal(contents, &loaded); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if loaded.DataDir != "" {
		cfg = Defaults(normalizePath(loaded.DataDir, filepath.Dir(file)))
		loaded.DataDir = cfg.DataDir
	}
	merge(&cfg, loaded)
	normalizeConfigPaths(&cfg)
	return cfg, nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func defaultDataDir() string {
	if dir := os.Getenv("RELAYHUB_DATA_DIR"); dir != "" {
		return dir
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "relayhub")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "relayhub")
	}
	return filepath.Join(".", ".relayhub")
}

func normalizePath(path, base string) string {
	if path == "" {
		return path
	}
	if !filepath.IsAbs(path) && base != "" {
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func normalizeConfigPaths(cfg *Config) {
	cfg.DataDir = normalizePath(cfg.DataDir, "")
	cfg.DatabasePath = normalizePath(cfg.DatabasePath, cfg.DataDir)
	cfg.BackupDir = normalizePath(cfg.BackupDir, cfg.DataDir)
	cfg.ExportDir = normalizePath(cfg.ExportDir, cfg.DataDir)
	cfg.LogsDir = normalizePath(cfg.LogsDir, cfg.DataDir)
	cfg.RuntimeDir = normalizePath(cfg.RuntimeDir, cfg.DataDir)
}

func merge(dst *Config, src Config) {
	if src.DataDir != "" {
		dst.DataDir = src.DataDir
	}
	if src.DatabasePath != "" {
		dst.DatabasePath = src.DatabasePath
	}
	if src.BackupDir != "" {
		dst.BackupDir = src.BackupDir
	}
	if src.ExportDir != "" {
		dst.ExportDir = src.ExportDir
	}
	if src.LogsDir != "" {
		dst.LogsDir = src.LogsDir
	}
	if src.RuntimeDir != "" {
		dst.RuntimeDir = src.RuntimeDir
	}
	if src.HTTPProxyAddr != "" {
		dst.HTTPProxyAddr = src.HTTPProxyAddr
	}
	if src.SOCKS5Addr != "" {
		dst.SOCKS5Addr = src.SOCKS5Addr
	}
	if src.GatewayAddr != "" {
		dst.GatewayAddr = src.GatewayAddr
	}
	if src.ManagementAddr != "" {
		dst.ManagementAddr = src.ManagementAddr
	}
	if src.HTTPProxyTargetPolicy != "" {
		dst.HTTPProxyTargetPolicy = src.HTTPProxyTargetPolicy
	}
	if src.SOCKS5TargetPolicy != "" {
		dst.SOCKS5TargetPolicy = src.SOCKS5TargetPolicy
	}
	if src.ManagementToken != "" {
		dst.ManagementToken = src.ManagementToken
	}
	if src.ManagementAuth != "" {
		dst.ManagementAuth = src.ManagementAuth
	}
	if src.ProxyUsername != "" {
		dst.ProxyUsername = src.ProxyUsername
	}
	if src.ProxyPassword != "" {
		dst.ProxyPassword = src.ProxyPassword
	}
	if src.MasterKeyStore != "" {
		dst.MasterKeyStore = src.MasterKeyStore
	}
	if src.EgressProxyURL != "" {
		dst.EgressProxyURL = src.EgressProxyURL
	}
	if src.Notify.WebhookURL != "" {
		dst.Notify.WebhookURL = src.Notify.WebhookURL
	}
	if src.Notify.BarkURL != "" {
		dst.Notify.BarkURL = src.Notify.BarkURL
	}
	if src.Notify.TelegramBotToken != "" {
		dst.Notify.TelegramBotToken = src.Notify.TelegramBotToken
	}
	if src.Notify.TelegramChatID != "" {
		dst.Notify.TelegramChatID = src.Notify.TelegramChatID
	}
	if src.Notify.QuotaLowUSD != 0 {
		dst.Notify.QuotaLowUSD = src.Notify.QuotaLowUSD
	}
	if src.VerifyProbeInterval != "" {
		dst.VerifyProbeInterval = src.VerifyProbeInterval
	}
	if src.HealthProbeInterval != "" {
		dst.HealthProbeInterval = src.HealthProbeInterval
	}
	if src.StreamCommitWindow != "" {
		dst.StreamCommitWindow = src.StreamCommitWindow
	}
	if src.BillingReconcileInterval != "" {
		dst.BillingReconcileInterval = src.BillingReconcileInterval
	}
}

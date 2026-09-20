package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// Proxy target policies: "open" allows public internet targets (default for out-of-the-box operation);
	// "local_only" limits targets to loopback/link-local.
	HTTPProxyTargetPolicy string `json:"http_proxy_target_policy"`
	SOCKS5TargetPolicy    string `json:"socks5_target_policy"`
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
}

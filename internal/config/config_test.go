package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir == "" || c.HTTPProxyAddr != "127.0.0.1:8787" || c.SOCKS5Addr != "127.0.0.1:8788" || c.GatewayAddr != "127.0.0.1:8789" || c.ManagementAddr != "127.0.0.1:8790" || c.HTTPProxyTargetPolicy != "open" || c.SOCKS5TargetPolicy != "open" {
		t.Fatalf("unexpected config: %#v", c)
	}
}
func TestProxyTargetPolicyIsExplicitAndRoundTrips(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "relayhub.json")
	if err := os.WriteFile(path, []byte(`{"http_proxy_target_policy":"open","socks5_target_policy":"local_only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPProxyTargetPolicy != "open" || c.SOCKS5TargetPolicy != "local_only" {
		t.Fatalf("proxy policy configuration was not preserved: %#v", c)
	}
}

func TestLoadUsesPlatformDefaultWhenPathIsEmpty(t *testing.T) {
	defaultDir := filepath.Join(t.TempDir(), "default-data")
	t.Setenv("RELAYHUB_DATA_DIR", defaultDir)

	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != defaultDir || c.DatabasePath != filepath.Join(defaultDir, "relayhub.db") {
		t.Fatalf("unexpected default paths: %#v", c)
	}
}

func TestLoadNormalizesConfiguredPathsRelativeToDataDir(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "relayhub.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(Config{
		DataDir:      "../data",
		DatabasePath: "db/custom.sqlite",
		BackupDir:    "backups",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, contents, 0600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "data")
	if c.DataDir != dataDir || c.DatabasePath != filepath.Join(dataDir, "db", "custom.sqlite") || c.BackupDir != filepath.Join(dataDir, "backups") {
		t.Fatalf("paths were not normalized: %#v", c)
	}
}

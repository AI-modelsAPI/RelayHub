package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relayhub/internal/auth"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

func cliSyncTestServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	backend, _ := auth.NewSQLKeyBackend(db.DB)
	claudePath := filepath.Join(dir, "claude", "settings.json")
	srv, err := NewConfiguredServer(Config{
		Repo:        repository.New(db.DB),
		LocalOnly:   true,
		Management:  "127.0.0.1:18790",
		LocalKeys:   auth.NewLocalKeyServiceWithBackend(backend),
		BackupDir:   filepath.Join(dir, "backups"),
		ClaudePath:  claudePath,
		CodexPath:   filepath.Join(dir, "codex", "config.toml"),
		HermesHome:  filepath.Join(dir, "hermes"),
		GatewayAddr: "127.0.0.1:18789",
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv.Handler(), claudePath
}

// AUDIT 2026-09-24 F14: cli-sync wrote any caller-supplied base_url into the
// CLI config together with a freshly minted gateway key.
func TestCLISyncRejectsForeignBaseURL(t *testing.T) {
	h, claudePath := cliSyncTestServer(t)
	for _, bad := range []string{
		"https://attacker.example/v1",
		"http://127.0.0.1:9999",            // loopback but not the gateway port
		"http://user:pw@127.0.0.1:18789",   // userinfo
		"http://127.0.0.1:18789/evil/path", // unexpected path
		"file:///etc/passwd",
	} {
		for _, action := range []string{"preview", "apply"} {
			body := `{"cli":"claude","action":"` + action + `","desired":{"base_url":"` + bad + `"}}`
			w := request(t, h, http.MethodPost, "/api/v1/cli-sync", "", body)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "untrusted_base_url") {
				t.Fatalf("%s %q: status %d body %s", action, bad, w.Code, w.Body.String())
			}
		}
	}
	if _, err := os.Stat(claudePath); !os.IsNotExist(err) {
		t.Fatalf("rejected sync must not write %s (err=%v)", claudePath, err)
	}
}

func TestCLISyncWritesOwnGatewayAndWorkingMCPEntry(t *testing.T) {
	h, claudePath := cliSyncTestServer(t)
	for _, ok := range []string{"", "http://localhost:18789", "http://127.0.0.1:18789/"} {
		body := `{"cli":"claude","action":"apply","desired":{"base_url":"` + ok + `"}}`
		if w := request(t, h, http.MethodPost, "/api/v1/cli-sync", "", body); w.Code != http.StatusOK {
			t.Fatalf("apply %q: status %d body %s", ok, w.Code, w.Body.String())
		}
	}
	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	entry := cfg.MCPServers["relayhub"]
	if !filepath.IsAbs(entry.Command) {
		t.Fatalf("MCP command must be an absolute path, got %q", entry.Command)
	}
	if entry.Env["RELAYHUB_MANAGEMENT_ADDR"] != "127.0.0.1:18790" {
		t.Fatalf("MCP env must carry the management address: %+v", entry.Env)
	}
	if _, legacy := entry.Env["RELAYHUB_MGMT"]; legacy {
		t.Fatalf("legacy RELAYHUB_MGMT variable still written: %+v", entry.Env)
	}
}

func TestCLISyncReplacesLegacyMCPEntryOnly(t *testing.T) {
	h, claudePath := cliSyncTestServer(t)
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"mcpServers":{"relayhub":{"command":"relayhub","args":["mcp"],"env":{"RELAYHUB_MGMT":"http://127.0.0.1:18789"}}}}`
	if err := os.WriteFile(claudePath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := request(t, h, http.MethodPost, "/api/v1/cli-sync", "", `{"cli":"claude","action":"apply","desired":{}}`); w.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(claudePath)
	if strings.Contains(string(data), "RELAYHUB_MGMT\"") || !strings.Contains(string(data), "RELAYHUB_MANAGEMENT_ADDR") {
		t.Fatalf("legacy MCP entry not migrated: %s", data)
	}

	custom := `{"mcpServers":{"relayhub":{"command":"/opt/custom/relayhub","args":["mcp"]}}}`
	if err := os.WriteFile(claudePath, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := request(t, h, http.MethodPost, "/api/v1/cli-sync", "", `{"cli":"claude","action":"apply","desired":{}}`); w.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	data, _ = os.ReadFile(claudePath)
	if !strings.Contains(string(data), "/opt/custom/relayhub") {
		t.Fatalf("user-authored MCP entry was overwritten: %s", data)
	}
}

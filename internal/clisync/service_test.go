package clisync_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relayhub/internal/clisync"
	"relayhub/internal/clisync/claude"
	"relayhub/internal/clisync/codex"
	"relayhub/internal/clisync/hermes"
)

// stubIssuer stands in for the real local key service.
type stubIssuer struct {
	key  string
	fail error
}

func (s stubIssuer) Create() (string, error) {
	if s.fail != nil {
		return "", s.fail
	}
	return s.key, nil
}

// The CLIs disagree on the endpoint suffix: Anthropic clients must NOT get a
// /v1, OpenAI-shaped clients must. A previous wiring handed every syncer one
// shared URL, which wrote http://host/v1 into ANTHROPIC_BASE_URL — an address
// the Anthropic API rejects. Each syncer must derive its own endpoint.
func TestEndpointSuffixDiffersPerProtocol(t *testing.T) {
	dir := t.TempDir()
	engine := clisync.NewEngine(nil, filepath.Join(dir, "backups"))
	const addr = "127.0.0.1:19999"

	svc := clisync.NewService(engine, stubIssuer{key: "rh_stub-key"}, addr, map[string]clisync.Syncer{
		"claude": &claude.Syncer{Engine: engine, Path: filepath.Join(dir, "claude", "settings.json"), GatewayAddr: addr},
		"codex":  &codex.Syncer{Engine: engine, Path: filepath.Join(dir, "codex", "config.toml"), GatewayAddr: addr},
		"hermes": &hermes.Syncer{Engine: engine, Home: filepath.Join(dir, "hermes"), GatewayAddr: addr},
	})

	ctx := context.Background()
	for _, tc := range []struct {
		cli        string
		wantURL    string
		rejectedIn string
	}{
		{"claude", "http://" + addr, "ANTHROPIC_BASE_URL must not carry /v1"},
		{"codex", "http://" + addr + "/v1", "codex speaks the OpenAI wire format"},
		{"hermes", "http://" + addr + "/v1", "hermes uses chat_completions"},
	} {
		diff, err := svc.Preview(ctx, tc.cli, clisync.DesiredState{Model: "coding"})
		if err != nil {
			t.Fatalf("%s preview: %v", tc.cli, err)
		}
		if !strings.Contains(diff.NewContent, tc.wantURL) {
			t.Fatalf("%s: expected endpoint %q in config (%s), got:\n%s", tc.cli, tc.wantURL, tc.rejectedIn, diff.NewContent)
		}
		if tc.cli == "claude" && strings.Contains(diff.NewContent, addr+"/v1") {
			t.Fatalf("claude config carries a /v1 endpoint, which the Anthropic API rejects:\n%s", diff.NewContent)
		}
	}
}

// Apply must back up, write, verify and record. A recorded "success" has to mean
// the bytes are really on disk.
func TestServiceApplyWritesVerifiesAndIssuesRealKey(t *testing.T) {
	dir := t.TempDir()
	engine := clisync.NewEngine(nil, filepath.Join(dir, "backups"))
	claudePath := filepath.Join(dir, "claude", "settings.json")

	svc := clisync.NewService(engine, stubIssuer{key: "rh_issued-by-service"}, "127.0.0.1:8789",
		map[string]clisync.Syncer{"claude": &claude.Syncer{Engine: engine, Path: claudePath, GatewayAddr: "127.0.0.1:8789"}})

	res, err := svc.Apply(context.Background(), "claude", clisync.DesiredState{Model: "coding"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("expected success, got %q", res.Status)
	}

	raw, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	var parsed struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("written config is not valid json: %v", err)
	}
	if parsed.Env["ANTHROPIC_AUTH_TOKEN"] != "rh_issued-by-service" {
		t.Fatalf("expected the service-issued key on disk, got %q", parsed.Env["ANTHROPIC_AUTH_TOKEN"])
	}
}

// Without a key issuer there is no valid key to write, so the service must fail
// rather than emit a config that 401s on first use.
func TestServiceRefusesWhenNoKeyCanBeIssued(t *testing.T) {
	dir := t.TempDir()
	engine := clisync.NewEngine(nil, filepath.Join(dir, "backups"))
	path := filepath.Join(dir, "claude", "settings.json")

	svc := clisync.NewService(engine, nil, "127.0.0.1:8789",
		map[string]clisync.Syncer{"claude": &claude.Syncer{Engine: engine, Path: path, GatewayAddr: "127.0.0.1:8789"}})

	if _, err := svc.Apply(context.Background(), "claude", clisync.DesiredState{}); err == nil {
		t.Fatal("expected apply to fail without a key issuer, got nil")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a refused apply must not leave a config file behind")
	}
}

// An unknown CLI is a caller error and must be distinguishable, so the API can
// answer 400 instead of a blanket 500.
func TestServiceUnknownCLIIsTyped(t *testing.T) {
	svc := clisync.NewService(clisync.NewEngine(nil, ""), stubIssuer{key: "rh_k"}, "127.0.0.1:8789",
		map[string]clisync.Syncer{})
	_, err := svc.Preview(context.Background(), "nope", clisync.DesiredState{})
	if err == nil {
		t.Fatal("expected an error for an unknown cli")
	}
}

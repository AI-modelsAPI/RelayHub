package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"relayhub/internal/clisync"
)

type Syncer struct {
	Engine *clisync.Engine
	Path   string

	// GatewayAddr is the gateway host:port. Claude Code speaks the Anthropic
	// API, whose base URL must NOT carry a /v1 suffix, so the endpoint is built
	// here rather than accepting one URL shared with the OpenAI-shaped CLIs.
	GatewayAddr string
}

func New(e *clisync.Engine, customPath string) *Syncer {
	return &Syncer{Engine: e, Path: customPath}
}

// endpoint renders the Anthropic-compatible gateway base URL.
func (s *Syncer) endpoint() string {
	addr := s.GatewayAddr
	if addr == "" {
		addr = "127.0.0.1:8789"
	}
	return "http://" + addr
}

func (s *Syncer) ConfigPath() string {
	if s.Path != "" {
		return s.Path
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func (s *Syncer) Detect(ctx context.Context) (clisync.Target, error) {
	p := s.ConfigPath()
	_, err := os.Stat(p)
	return clisync.Target{
		CLI:        "claude-code",
		ConfigPath: p,
		Exists:     err == nil,
	}, nil
}

func (s *Syncer) Preview(ctx context.Context, desired clisync.DesiredState) (clisync.Diff, error) {
	p := s.ConfigPath()
	target, _ := s.Detect(ctx)

	var current map[string]interface{}
	var oldBytes []byte

	if target.Exists {
		data, err := os.ReadFile(p)
		if err != nil {
			return clisync.Diff{}, err
		}
		oldBytes = data
		if err := json.Unmarshal(data, &current); err != nil {
			return clisync.Diff{}, fmt.Errorf("invalid json in claude settings: %w", err)
		}
	} else {
		current = make(map[string]interface{})
	}

	// Prepare env sub-object
	envObj, ok := current["env"].(map[string]interface{})
	if !ok {
		envObj = make(map[string]interface{})
		current["env"] = envObj
	}

	changedKeys := []string{}
	baseURL := desired.BaseURL
	if baseURL == "" {
		baseURL = s.endpoint() // Anthropic endpoint without /v1
	}

	if envObj["ANTHROPIC_BASE_URL"] != baseURL {
		envObj["ANTHROPIC_BASE_URL"] = baseURL
		changedKeys = append(changedKeys, "env.ANTHROPIC_BASE_URL")
	}
	if desired.APIKey != "" && envObj["ANTHROPIC_AUTH_TOKEN"] != desired.APIKey {
		envObj["ANTHROPIC_AUTH_TOKEN"] = desired.APIKey
		changedKeys = append(changedKeys, "env.ANTHROPIC_AUTH_TOKEN")
	}
	if desired.Model != "" && envObj["ANTHROPIC_DEFAULT_SONNET_MODEL"] != desired.Model {
		envObj["ANTHROPIC_DEFAULT_SONNET_MODEL"] = desired.Model
		changedKeys = append(changedKeys, "env.ANTHROPIC_DEFAULT_SONNET_MODEL")
	}

	mcp, _ := current["mcpServers"].(map[string]interface{})
	if mcp == nil {
		mcp = map[string]interface{}{}
		current["mcpServers"] = mcp
	}
	if _, ok := mcp["relayhub"]; !ok {
		mcp["relayhub"] = map[string]interface{}{
			"command": "relayhub",
			"args":    []string{"mcp"},
			"env":     map[string]interface{}{"RELAYHUB_MGMT": s.endpoint()},
		}
		changedKeys = append(changedKeys, "mcpServers.relayhub")
	}

	newBytes, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return clisync.Diff{}, err
	}

	return clisync.Diff{
		Target:      target,
		OldContent:  string(oldBytes),
		NewContent:  string(newBytes),
		ChangedKeys: changedKeys,
		HasChanges:  len(changedKeys) > 0,
	}, nil
}

func (s *Syncer) Apply(ctx context.Context, diff clisync.Diff) (clisync.Backup, error) {
	var b clisync.Backup
	if diff.Target.Exists {
		var err error
		b, err = clisync.CreateBackup(diff.Target.ConfigPath, s.Engine.BackupDir)
		if err != nil {
			return clisync.Backup{}, err
		}
	}

	err := s.Engine.WriteAtomic(diff.Target.ConfigPath, []byte(diff.NewContent))
	if err != nil {
		return b, err
	}
	return b, nil
}

func (s *Syncer) Verify(ctx context.Context, desired clisync.DesiredState) error {
	p := s.ConfigPath()
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var current map[string]interface{}
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	envObj, ok := current["env"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("env section missing in %s", p)
	}
	expectedURL := desired.BaseURL
	if expectedURL == "" {
		expectedURL = s.endpoint()
	}
	if envObj["ANTHROPIC_BASE_URL"] != expectedURL {
		return fmt.Errorf("ANTHROPIC_BASE_URL mismatch: got %v, expected %v", envObj["ANTHROPIC_BASE_URL"], expectedURL)
	}
	return nil
}

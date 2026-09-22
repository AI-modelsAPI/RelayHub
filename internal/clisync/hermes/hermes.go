package hermes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"relayhub/internal/clisync"
)

// envKeyName is the variable Hermes reads the provider credential from.
const envKeyName = "HERMES_CUSTOM_RELAYHUB_API_KEY"

type Syncer struct {
	Engine *clisync.Engine
	Home   string

	// GatewayAddr is the gateway host:port; Hermes uses the OpenAI-compatible
	// chat_completions transport, so its endpoint carries the /v1 suffix.
	GatewayAddr string
}

func New(e *clisync.Engine, customHome string) *Syncer {
	return &Syncer{Engine: e, Home: customHome}
}

// endpoint renders the OpenAI-compatible gateway base URL.
func (s *Syncer) endpoint() string {
	addr := s.GatewayAddr
	if addr == "" {
		addr = "127.0.0.1:8789"
	}
	return "http://" + addr + "/v1"
}

func (s *Syncer) HermesHome() string {
	if s.Home != "" {
		return s.Home
	}
	if dir := os.Getenv("HERMES_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hermes")
}

func (s *Syncer) ConfigPath() string {
	return filepath.Join(s.HermesHome(), "config.yaml")
}

func (s *Syncer) EnvPath() string {
	return filepath.Join(s.HermesHome(), ".env")
}

func (s *Syncer) Detect(ctx context.Context) (clisync.Target, error) {
	p := s.ConfigPath()
	_, err := os.Stat(p)
	return clisync.Target{
		CLI:        "hermes",
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
		if err := yaml.Unmarshal(data, &current); err != nil {
			return clisync.Diff{}, fmt.Errorf("invalid yaml in hermes config: %w", err)
		}
	} else {
		current = make(map[string]interface{})
	}

	changedKeys := []string{}
	baseURL := desired.BaseURL
	if baseURL == "" {
		baseURL = s.endpoint()
	}
	model := desired.Model
	if model == "" {
		model = "coding"
	}

	// 1. Configure provider under providers.relayhub
	providers, ok := current["providers"].(map[string]interface{})
	if !ok {
		providers = make(map[string]interface{})
		current["providers"] = providers
	}

	relayhubProvider, ok := providers["relayhub"].(map[string]interface{})
	if !ok {
		relayhubProvider = make(map[string]interface{})
		providers["relayhub"] = relayhubProvider
	}

	relayhubProvider["name"] = "RelayHub"
	relayhubProvider["base_url"] = baseURL
	relayhubProvider["key_env"] = envKeyName
	relayhubProvider["transport"] = "chat_completions"
	relayhubProvider["model"] = model
	changedKeys = append(changedKeys, "providers.relayhub")

	// 2. Optionally set as default model provider
	if desired.Provider == "relayhub" {
		modelSection, ok := current["model"].(map[string]interface{})
		if !ok {
			modelSection = make(map[string]interface{})
			current["model"] = modelSection
		}
		modelSection["provider"] = "relayhub"
		modelSection["default"] = model
		modelSection["base_url"] = baseURL
		changedKeys = append(changedKeys, "model.provider", "model.default")
	}

	newBytes, err := yaml.Marshal(current)
	if err != nil {
		return clisync.Diff{}, err
	}

	return clisync.Diff{
		Target:      target,
		APIKey:      desired.APIKey,
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

	// A placeholder key would produce a config that looks synced but fails
	// with 401 on first use, so refuse before writing anything.
	if clisync.IsPlaceholderKey(diff.APIKey) {
		return b, clisync.ErrNoRealAPIKey
	}

	// Write config.yaml
	err := s.Engine.WriteAtomic(diff.Target.ConfigPath, []byte(diff.NewContent))
	if err != nil {
		return b, err
	}

	// The key lives in .env only, never in config.yaml.
	if err := s.Engine.UpsertEnvVar(s.EnvPath(), envKeyName, diff.APIKey); err != nil {
		return b, fmt.Errorf("write hermes .env: %w", err)
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
	if err := yaml.Unmarshal(data, &current); err != nil {
		return err
	}
	providers, ok := current["providers"].(map[string]interface{})
	if !ok || providers["relayhub"] == nil {
		return fmt.Errorf("relayhub provider missing in %s", p)
	}
	env, err := os.ReadFile(s.EnvPath())
	if err != nil {
		return fmt.Errorf("hermes .env missing (%s): %w", s.EnvPath(), err)
	}
	if !clisync.HasEnvVar(string(env), envKeyName) {
		return fmt.Errorf("%s is not set in %s", envKeyName, s.EnvPath())
	}
	return nil
}

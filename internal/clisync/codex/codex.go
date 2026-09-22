package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
	"relayhub/internal/clisync"
)

// envKeyName is the environment variable config.toml points Codex at for the
// RelayHub provider credential.
const envKeyName = "RELAYHUB_API_KEY"

type Syncer struct {
	Engine *clisync.Engine
	Path   string

	// GatewayAddr is the gateway host:port; Codex speaks the OpenAI wire
	// format, so its endpoint carries the /v1 suffix.
	GatewayAddr string
}

func New(e *clisync.Engine, customPath string) *Syncer {
	return &Syncer{Engine: e, Path: customPath}
}

// endpoint renders the OpenAI-compatible gateway base URL.
func (s *Syncer) endpoint() string {
	addr := s.GatewayAddr
	if addr == "" {
		addr = "127.0.0.1:8789"
	}
	return "http://" + addr + "/v1"
}

func (s *Syncer) ConfigPath() string {
	if s.Path != "" {
		return s.Path
	}
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return filepath.Join(dir, "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "config.toml")
}

// EnvPath is the dotenv file Codex CLI loads at startup (`$CODEX_HOME/.env`,
// filtered so it cannot set CODEX_* variables). config.toml only names the
// variable via model_providers.relayhub.env_key; the key itself must be
// delivered here or Codex fails auth on first use (AUDIT RH-23).
func (s *Syncer) EnvPath() string {
	return filepath.Join(filepath.Dir(s.ConfigPath()), ".env")
}

func (s *Syncer) Detect(ctx context.Context) (clisync.Target, error) {
	p := s.ConfigPath()
	_, err := os.Stat(p)
	return clisync.Target{
		CLI:        "codex",
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
		if err := toml.Unmarshal(data, &current); err != nil {
			return clisync.Diff{}, fmt.Errorf("invalid toml in codex config: %w", err)
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

	if current["model"] != model {
		current["model"] = model
		changedKeys = append(changedKeys, "model")
	}
	if current["model_provider"] != "relayhub" {
		current["model_provider"] = "relayhub"
		changedKeys = append(changedKeys, "model_provider")
	}

	modelProviders, ok := current["model_providers"].(map[string]interface{})
	if !ok {
		modelProviders = make(map[string]interface{})
		current["model_providers"] = modelProviders
	}

	relayhubProvider, ok := modelProviders["relayhub"].(map[string]interface{})
	if !ok {
		relayhubProvider = make(map[string]interface{})
		modelProviders["relayhub"] = relayhubProvider
	}

	relayhubProvider["name"] = "RelayHub"
	relayhubProvider["base_url"] = baseURL
	relayhubProvider["wire_api"] = "chat"
	relayhubProvider["env_key"] = envKeyName
	changedKeys = append(changedKeys, "model_providers.relayhub")

	// The key never enters config.toml; it is written to EnvPath on apply.
	changedKeys = append(changedKeys, ".env:"+envKeyName)

	newBytes, err := toml.Marshal(current)
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

	// Refuse before touching any file: a config.toml that names env_key
	// without the variable being delivered is a sync that 401s on first use.
	if clisync.IsPlaceholderKey(diff.APIKey) {
		return b, clisync.ErrNoRealAPIKey
	}

	err := s.Engine.WriteAtomic(diff.Target.ConfigPath, []byte(diff.NewContent))
	if err != nil {
		return b, err
	}
	if err := s.Engine.UpsertEnvVar(s.EnvPath(), envKeyName, diff.APIKey); err != nil {
		return b, fmt.Errorf("write codex .env: %w", err)
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
	if err := toml.Unmarshal(data, &current); err != nil {
		return err
	}
	if current["model_provider"] != "relayhub" {
		return fmt.Errorf("model_provider mismatch: got %v", current["model_provider"])
	}
	env, err := os.ReadFile(s.EnvPath())
	if err != nil {
		return fmt.Errorf("codex .env missing (%s): %w", s.EnvPath(), err)
	}
	if !clisync.HasEnvVar(string(env), envKeyName) {
		return fmt.Errorf("%s is not set in %s", envKeyName, s.EnvPath())
	}
	return nil
}

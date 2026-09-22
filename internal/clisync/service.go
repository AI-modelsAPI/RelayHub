package clisync

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"

	"relayhub/internal/domain"
)

// ErrUnknownCLI is returned when a caller names a CLI that has no registered syncer.
var ErrUnknownCLI = errors.New("unknown cli target")

// KeyIssuer issues a real local gateway API key. CLI config files are useless
// without a key the gateway will actually accept, and writing a placeholder
// there was a confirmed defect (AUDIT-REPORT P1-6), so the service always goes
// through the production key service instead of inventing a literal.
type KeyIssuer interface {
	Create() (string, error)
}

// Service turns the per-CLI syncers into one API-facing surface: detect, preview,
// apply and verify. It owns the parts that must not be duplicated per handler —
// issuing a real key, backing up before writing, verifying after writing,
// restoring the backup when verification fails, and recording every attempt.
type Service struct {
	engine  *Engine
	syncers map[string]Syncer
	keys    KeyIssuer

	// GatewayAddr is the gateway's host:port. The per-CLI endpoint is derived
	// from it rather than stored as one URL, because the CLIs disagree on the
	// suffix: OpenAI-shaped clients want /v1, Anthropic-shaped clients must not
	// have it. Letting the service impose a single URL wrote a /v1 endpoint into
	// Claude Code's ANTHROPIC_BASE_URL, which the Anthropic API rejects.
	GatewayAddr string
}

// NewService builds a service over the given syncers. Keys may be nil, in which
// case preview/apply report that no key can be issued rather than fabricating one.
func NewService(engine *Engine, keys KeyIssuer, gatewayAddr string, syncers map[string]Syncer) *Service {
	return &Service{engine: engine, syncers: syncers, keys: keys, GatewayAddr: gatewayAddr}
}

// Targets reports the detected state of every registered CLI.
func (s *Service) Targets(ctx context.Context) ([]Target, error) {
	if s == nil {
		return nil, errors.New("cli sync service is not configured")
	}
	out := make([]Target, 0, len(s.syncers))
	for _, name := range s.names() {
		t, err := s.syncers[name].Detect(ctx)
		if err != nil {
			return nil, fmt.Errorf("detect %s: %w", name, err)
		}
		out = append(out, t)
	}
	return out, nil
}

// names returns CLI keys in a stable order so API responses do not shuffle
// between calls (Go map iteration is deliberately randomised).
func (s *Service) names() []string {
	ordered := []string{"claude", "codex", "hermes"}
	seen := make(map[string]bool, len(ordered))
	out := make([]string, 0, len(s.syncers))
	for _, n := range ordered {
		if _, ok := s.syncers[n]; ok {
			out = append(out, n)
			seen[n] = true
		}
	}
	for n := range s.syncers {
		if !seen[n] {
			out = append(out, n)
		}
	}
	return out
}

// Preview computes the change set for one CLI without touching disk. It issues a
// real gateway key so the caller can see exactly what would be written.
func (s *Service) Preview(ctx context.Context, cli string, desired DesiredState) (Diff, error) {
	syncer, err := s.syncer(cli)
	if err != nil {
		return Diff{}, err
	}
	// Preview must not mint a real gateway key: an abandoned preview would
	// leave a live credential behind, and even an applied one would be
	// orphaned if Apply were never called (AUDIT RH-23/24). A marker keeps the
	// diff honest without touching the key service.
	if desired.APIKey == "" {
		desired.APIKey = "<<issued-on-apply>>"
	}
	return syncer.Preview(ctx, desired)
}

// Apply writes the change set for one CLI. The sequence is deliberate: back up
// the current file, write atomically, read back and verify, and restore the
// backup if verification fails. Every outcome — including failure — is recorded,
// so the UI can never claim success for a write that did not happen.
func (s *Service) Apply(ctx context.Context, cli string, desired DesiredState) (Result, error) {
	syncer, err := s.syncer(cli)
	if err != nil {
		return Result{}, err
	}
	desired, err = s.resolveDesired(desired)
	if err != nil {
		return Result{}, err
	}

	diff, err := syncer.Preview(ctx, desired)
	if err != nil {
		return Result{}, err
	}

	backup, err := syncer.Apply(ctx, diff)
	if err != nil {
		s.record(ctx, cli, diff.Target.ConfigPath, backup.BackupPath, "failed", err)
		return Result{}, err
	}

	if verifyErr := syncer.Verify(ctx, desired); verifyErr != nil {
		restoreErr := restoreBackup(backup)
		s.record(ctx, cli, diff.Target.ConfigPath, backup.BackupPath, "failed", verifyErr)
		if restoreErr != nil {
			return Result{}, fmt.Errorf("verification failed (%v) and backup restore failed: %w", verifyErr, restoreErr)
		}
		return Result{}, fmt.Errorf("post-write verification failed, original config restored: %w", verifyErr)
	}

	s.record(ctx, cli, diff.Target.ConfigPath, backup.BackupPath, "success", nil)

	return Result{
		CLI:    cli,
		Status: "success",
		Diff:   diff,
		Backup: backup,
	}, nil
}

// Verify re-reads the on-disk config and reports whether it still matches the
// desired state.
func (s *Service) Verify(ctx context.Context, cli string, desired DesiredState) error {
	syncer, err := s.syncer(cli)
	if err != nil {
		return err
	}
	// Verification must not mint a new key: it only inspects what is on disk,
	// and the syncer supplies its own endpoint default.
	return syncer.Verify(ctx, desired)
}

// Result is the outcome of a successful apply.
type Result struct {
	CLI    string `json:"cli"`
	Status string `json:"status"`
	Diff   Diff   `json:"diff"`
	Backup Backup `json:"backup"`
}

func (s *Service) syncer(cli string) (Syncer, error) {
	if s == nil || len(s.syncers) == 0 {
		return nil, errors.New("cli sync service is not configured")
	}
	syncer, ok := s.syncers[cli]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCLI, cli)
	}
	return syncer, nil
}

// resolveDesired fills in the gateway base URL and issues a real API key. An
// empty key is an error rather than a silent placeholder: a CLI pointed at the
// gateway with a bogus key fails at request time with a confusing 401.
func (s *Service) resolveDesired(desired DesiredState) (DesiredState, error) {
	if desired.APIKey == "" {
		if s.keys == nil {
			return desired, errors.New("local key service is not configured; cannot issue a gateway api key")
		}
		key, err := s.keys.Create()
		if err != nil {
			return desired, fmt.Errorf("issue local api key: %w", err)
		}
		desired.APIKey = key
	}
	return desired, nil
}

func (s *Service) record(ctx context.Context, cli, configPath, backupPath, status string, cause error) {
	if s.engine == nil {
		return
	}
	rec := domain.CLISyncRecord{
		ID:         uuid.NewString(),
		CLI:        cli,
		ConfigPath: configPath,
		BackupPath: backupPath,
		Status:     status,
	}
	if cause != nil {
		rec.Error = cause.Error()
	}
	s.engine.RecordSync(ctx, rec)
}

// restoreBackup puts the pre-write file contents back. A no-op when the target
// did not exist before (nothing to restore to).
func restoreBackup(b Backup) error {
	if b.BackupPath == "" || b.OriginalPath == "" {
		return nil
	}
	data, err := os.ReadFile(b.BackupPath)
	if err != nil {
		return err
	}
	return os.WriteFile(b.OriginalPath, data, 0600)
}

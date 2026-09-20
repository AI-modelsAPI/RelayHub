package clisync

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
)

type Syncer interface {
	Detect(ctx context.Context) (Target, error)
	Preview(ctx context.Context, desired DesiredState) (Diff, error)
	Apply(ctx context.Context, diff Diff) (Backup, error)
	Verify(ctx context.Context, desired DesiredState) error
}

type Engine struct {
	Repo      repository.ResourceRepository
	BackupDir string
}

func NewEngine(repo repository.ResourceRepository, backupDir string) *Engine {
	return &Engine{
		Repo:      repo,
		BackupDir: backupDir,
	}
}

// WriteAtomic writes to a temporary file, fsyncs, and renames atomically to dest.
// If dest is a symlink, it is rejected for safety.
func (e *Engine) WriteAtomic(path string, content []byte) error {
	fi, err := os.Lstat(path)
	if err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("cannot write to symlink")
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, "relayhub-clisync-*")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(content); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}

func (e *Engine) RecordSync(ctx context.Context, rec domain.CLISyncRecord) {
	if e.Repo != nil {
		_ = e.Repo.CreateCLISyncRecord(ctx, rec)
	}
}

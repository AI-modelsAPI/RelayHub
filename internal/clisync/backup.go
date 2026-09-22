package clisync

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Backup struct {
	OriginalPath string    `json:"original_path"`
	BackupPath   string    `json:"backup_path"`
	SHA256       string    `json:"sha256"`
	Timestamp    time.Time `json:"timestamp"`
}

func CreateBackup(path string, backupDir string) (Backup, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Backup{}, err
	}
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	if backupDir == "" {
		backupDir = filepath.Join(filepath.Dir(path), "backups")
	}
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return Backup{}, err
	}

	ts := time.Now().UTC()
	// Second-resolution timestamps collide when two syncs run within the same
	// second; previously the later backup silently truncated the earlier one
	// (AUDIT RH-24). Use O_EXCL with a nanosecond+sequence suffix.
	base := fmt.Sprintf("%s.%s", filepath.Base(path), ts.Format("20060102150405"))
	var dest string
	for i := 0; ; i++ {
		candidate := filepath.Join(backupDir, fmt.Sprintf("%s.%06d.bak", base, time.Now().UnixNano()%1000000))
		if i > 0 {
			candidate = filepath.Join(backupDir, fmt.Sprintf("%s.%06d-%d.bak", base, time.Now().UnixNano()%1000000, i))
		}
		f, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			if i >= 8 {
				return Backup{}, fmt.Errorf("cannot allocate unique backup name: %w", err)
			}
			continue
		}
		if _, werr := f.Write(data); werr != nil {
			_ = f.Close()
			return Backup{}, werr
		}
		if cerr := f.Close(); cerr != nil {
			return Backup{}, cerr
		}
		dest = candidate
		break
	}

	return Backup{
		OriginalPath: path,
		BackupPath:   dest,
		SHA256:       hashStr,
		Timestamp:    ts,
	}, nil
}

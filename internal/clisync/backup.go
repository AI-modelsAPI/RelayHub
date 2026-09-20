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
	bName := fmt.Sprintf("%s.%s.bak", filepath.Base(path), ts.Format("20060102150405"))
	dest := filepath.Join(backupDir, bName)

	if err := os.WriteFile(dest, data, 0600); err != nil {
		return Backup{}, err
	}

	return Backup{
		OriginalPath: path,
		BackupPath:   dest,
		SHA256:       hashStr,
		Timestamp:    ts,
	}, nil
}

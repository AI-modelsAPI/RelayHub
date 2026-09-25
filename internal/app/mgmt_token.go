package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ManagementTokenFile is the auto-generated management API token inside the
// data directory. `relayhub pair`, `relayhub mcp` and the desktop shell read
// it from there.
const ManagementTokenFile = "management.token"

// resolveManagementToken decides the management API credential (AUDIT
// 2026-09-24 F4). An explicit token wins; mode "token" otherwise loads or
// creates <data-dir>/management.token; "off" disables authentication on the
// loopback management plane; "" keeps the embedding default (the explicit
// token, possibly empty) for library callers and tests. source describes
// where the token came from, for the startup log.
func resolveManagementToken(dataDir, mode, explicit string) (token, source string, err error) {
	explicit = strings.TrimSpace(explicit)
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		if explicit == "" {
			return "", "off", nil
		}
		return explicit, "config", nil
	case "token":
		if explicit != "" {
			return explicit, "config", nil
		}
		path := filepath.Join(dataDir, ManagementTokenFile)
		tok, err := loadOrCreateManagementToken(path)
		if err != nil {
			return "", "", fmt.Errorf("management token: %w", err)
		}
		return tok, path, nil
	case "off":
		if explicit != "" {
			return "", "", errors.New(`management_auth "off" conflicts with management_token; remove one of them`)
		}
		return "", "off", nil
	default:
		return "", "", fmt.Errorf(`unknown management_auth %q (want "token" or "off")`, mode)
	}
}

func loadOrCreateManagementToken(path string) (string, error) {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s must be a regular file", path)
		}
		restrictToOwner(path, 0o600)
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		tok := strings.TrimSpace(string(b))
		if err := validManagementToken(tok); err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		return tok, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return loadOrCreateManagementToken(path) // lost a creation race
		}
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(tok + "\n"); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return tok, nil
}

func validManagementToken(tok string) error {
	if len(tok) < 16 {
		return errors.New("the token must be at least 16 characters")
	}
	for _, c := range tok {
		if c <= ' ' || c == 0x7f {
			return errors.New("the token must not contain whitespace or control characters")
		}
	}
	return nil
}

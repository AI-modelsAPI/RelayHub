package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// sanitizeSegment transforms an arbitrary string into a deterministic, safe, collision-free path component.
//
// Key requirements:
// 1. Strict containment: cannot contain "/" or "\" or "..".
// 2. Collision-free for distinct raw inputs:
//   - "a/b" vs "a_b"
//   - "account" vs " account "
//   - "myAccount" vs "myaccount" (macOS / APFS is case-insensitive by default)
//
// 3. Length boundary: paths must not exceed filesystem limits.
//
// To achieve collision-resistance on case-insensitive filesystems and between
// sanitized aliases / raw inputs, every segment incorporates a stable hash of the EXACT,
// un-trimmed raw byte sequence:
// e.g. <human-prefix>_<sha256-hex>
// The human prefix is lowercased ASCII [a-z0-9_-] (max 24 chars) for readability.
// The hash suffix (16 hex chars = 64-bit cryptographic entropy of the raw string) ensures
// that ANY difference in raw characters, whitespace, case, slashes, or special symbols
// produces a distinct path component.
func sanitizeSegment(s string) string {
	rawHash := sha256.Sum256([]byte(s))
	hashSuffix := hex.EncodeToString(rawHash[:8]) // 16 hex chars

	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sb.WriteRune(r)
		} else if r >= 'A' && r <= 'Z' {
			// Lowercase for APFS / case-insensitive safety
			sb.WriteRune(r + ('a' - 'A'))
		} else {
			sb.WriteRune('_')
		}
		if sb.Len() >= 24 {
			break
		}
	}
	prefix := strings.Trim(sb.String(), "_")
	if prefix == "" || prefix == "." || prefix == ".." {
		prefix = "seg"
	}

	return fmt.Sprintf("%s_%s", prefix, hashSuffix)
}

// ProfileDir returns the dedicated profile directory for a provider and channel:
// <data-dir>/runtime/profiles/<provider>/<channel>/
// Input validation and sanitization ensures paths never escape data-dir.
func ProfileDir(dataDir, providerID, channelID string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", errors.New("dataDir must not be empty")
	}
	if strings.TrimSpace(providerID) == "" {
		return "", errors.New("providerID must not be empty")
	}
	if strings.TrimSpace(channelID) == "" {
		return "", errors.New("channelID must not be empty")
	}

	cleanProvider := sanitizeSegment(providerID)
	cleanChannel := sanitizeSegment(channelID)

	baseDir := filepath.Clean(filepath.Join(dataDir, "runtime", "profiles"))
	targetPath := filepath.Clean(filepath.Join(baseDir, cleanProvider, cleanChannel))

	// Defensive check: ensure targetPath is under baseDir
	rel, err := filepath.Rel(baseDir, targetPath)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return "", fmt.Errorf("profile path escapes base directory: %s", targetPath)
	}

	return targetPath, nil
}

package export

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"relayhub/internal/domain"
)

// CurrentPackageVersion is 2: version 2 adds a keyed (HMAC) integrity check
// for password-protected packages. Version 1 packages keep their legacy plain
// SHA-256 checksum and remain importable (AUDIT RH-13: an unkeyed checksum
// detects corruption but cannot detect tampering, since an attacker can
// recompute the hash after editing the payload).
const CurrentPackageVersion = 2

type Manifest struct {
	Version       int       `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	HasSecrets    bool      `json:"has_secrets"`
	ProviderCount int       `json:"provider_count"`
	ChannelCount  int       `json:"channel_count"`
	ModelCount    int       `json:"model_count"`
	RouteCount    int       `json:"route_count"`
	// v2 adds complete configuration migration coverage (AUDIT RH-13): model
	// bindings, fallback groups and channel-key metadata were previously lost
	// on export, silently breaking installs after re-import.
	ProviderModelCount int    `json:"provider_model_count,omitempty"`
	ModelGroupCount    int    `json:"model_group_count,omitempty"`
	ChannelKeyCount    int    `json:"channel_key_count,omitempty"`
	Checksum           string `json:"checksum"`
}

type PackageData struct {
	Manifest  Manifest          `json:"manifest"`
	Providers []domain.Provider `json:"providers"`
	Channels  []domain.Channel  `json:"channels"`
	Models    []domain.Model    `json:"models"`
	Routes    []domain.Route    `json:"routes"`
	Secrets   string            `json:"secrets,omitempty"` // Base64 encrypted if HasSecrets

	// omitempty keeps re-hashed v1 packages byte-identical when these lists are
	// empty, preserving legacy checksum compatibility.
	ProviderModels    []domain.ProviderModel    `json:"provider_models,omitempty"`
	ModelGroups       []domain.ModelGroup       `json:"model_groups,omitempty"`
	ModelGroupMembers []domain.ModelGroupMember `json:"model_group_members,omitempty"`
	ChannelKeys       []domain.ChannelKey       `json:"channel_keys,omitempty"`
}

type ConflictPolicy string

const (
	ConflictSkip      ConflictPolicy = "skip"
	ConflictOverwrite ConflictPolicy = "overwrite"
)

type ConflictItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Preview struct {
	Version       int            `json:"version"`
	CreatedAt     time.Time      `json:"created_at"`
	HasSecrets    bool           `json:"has_secrets"`
	TotalEntities int            `json:"total_entities"`
	Conflicts     []ConflictItem `json:"conflicts"`
}

type SecretImporter func(ctx context.Context, secrets map[string][]byte) error

// ComputePackageChecksum computes the SHA256 checksum over the PackageData with an empty Checksum field.
func ComputePackageChecksum(pkg PackageData) (string, error) {
	pkg.Manifest.Checksum = ""
	dataWithoutChecksum, err := json.Marshal(pkg)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(dataWithoutChecksum)
	return hex.EncodeToString(hash[:]), nil
}

// ComputePackageChecksumV2 computes an HMAC-SHA256 over the package with an
// empty Checksum field, keyed from the export password. An attacker who edits
// a v2 package cannot forge the digest without the password (AUDIT RH-13).
func ComputePackageChecksumV2(pkg PackageData, password string) (string, error) {
	pkg.Manifest.Checksum = ""
	dataWithoutChecksum, err := json.Marshal(pkg)
	if err != nil {
		return "", err
	}
	key := deriveKey(password, []byte("relayhub-export-checksum-v2"))
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(dataWithoutChecksum)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

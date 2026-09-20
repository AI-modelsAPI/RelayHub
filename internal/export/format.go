package export

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"relayhub/internal/domain"
)

const CurrentPackageVersion = 1

type Manifest struct {
	Version       int       `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	HasSecrets    bool      `json:"has_secrets"`
	ProviderCount int       `json:"provider_count"`
	ChannelCount  int       `json:"channel_count"`
	ModelCount    int       `json:"model_count"`
	RouteCount    int       `json:"route_count"`
	Checksum      string    `json:"checksum"`
}

type PackageData struct {
	Manifest  Manifest          `json:"manifest"`
	Providers []domain.Provider `json:"providers"`
	Channels  []domain.Channel  `json:"channels"`
	Models    []domain.Model    `json:"models"`
	Routes    []domain.Route    `json:"routes"`
	Secrets   string            `json:"secrets,omitempty"` // Base64 encrypted if HasSecrets
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

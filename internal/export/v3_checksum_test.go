package export

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

func exportTestRepo(t *testing.T) *repository.Store {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db.DB)
	ctx := context.Background()
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p1", Name: "P1", AdapterType: "generic", Enabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c1", ProviderID: "p1", Name: "C1", BaseURL: "https://relay.example.com", Enabled: true})
	return repo
}

// AUDIT 2026-09-24 F17: v2 keyed checksums used a fixed public salt; v3 uses
// a random per-package salt, and v2 packages remain importable.
func TestV3PackagesUseRandomSaltAndDetectTampering(t *testing.T) {
	repo := exportTestRepo(t)
	ctx := context.Background()
	const pw = "Correct-Horse-Battery-Staple"
	exporter := func(context.Context) (map[string][]byte, error) { return map[string][]byte{"ref": []byte("v")}, nil }
	a, err := Create(ctx, repo, ExportOptions{IncludeSecrets: true, Password: pw, SecretExporter: exporter})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Create(ctx, repo, ExportOptions{IncludeSecrets: true, Password: pw, SecretExporter: exporter})
	var pa, pb PackageData
	_ = json.Unmarshal(a, &pa)
	_ = json.Unmarshal(b, &pb)
	if pa.Manifest.Version != 3 || pa.Manifest.ChecksumSalt == "" || pa.Manifest.ChecksumSalt == pb.Manifest.ChecksumSalt {
		t.Fatalf("v3 salt missing or reused: %+v / %+v", pa.Manifest, pb.Manifest)
	}
	target := exportTestRepo(t)
	if err := ApplyWithOptions(ctx, a, *target, ApplyOptions{Policy: ConflictOverwrite, Password: pw}); err != nil {
		t.Fatalf("v3 import: %v", err)
	}

	tampered := strings.Replace(string(a), "https://relay.example.com", "https://attacker.example", 1)
	if err := ApplyWithOptions(ctx, []byte(tampered), *target, ApplyOptions{Policy: ConflictOverwrite, Password: pw}); !errors.Is(err, ErrTamperedPackage) {
		t.Fatalf("tampered v3 package accepted: %v", err)
	}
	pa.Manifest.ChecksumSalt = ""
	noSalt, _ := json.Marshal(pa)
	if err := ApplyWithOptions(ctx, noSalt, *target, ApplyOptions{Policy: ConflictOverwrite, Password: pw}); err == nil {
		t.Fatal("v3 package without salt accepted")
	}

	// Legacy v2 (fixed-salt) packages stay importable.
	var legacy PackageData
	_ = json.Unmarshal(a, &legacy)
	legacy.Manifest.Version = 2
	legacy.Manifest.ChecksumSalt = ""
	sum, err := ComputePackageChecksumV2(legacy, pw)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Manifest.Checksum = sum
	v2, _ := json.Marshal(legacy)
	if err := ApplyWithOptions(ctx, v2, *target, ApplyOptions{Policy: ConflictOverwrite, Password: pw}); err != nil {
		t.Fatalf("legacy v2 import: %v", err)
	}
}

func TestPreviewReportsIntegrityLevel(t *testing.T) {
	repo := exportTestRepo(t)
	ctx := context.Background()
	plain, _ := Create(ctx, repo, ExportOptions{})
	if p, err := PreviewPackage(ctx, plain, repo); err != nil || p.Integrity != "unauthenticated" {
		t.Fatalf("plain package preview: %+v %v", p, err)
	}
	keyed, _ := Create(ctx, repo, ExportOptions{IncludeSecrets: true, Password: "pw-123456"})
	if p, err := PreviewPackage(ctx, keyed, repo); err != nil || p.Integrity != "verified_on_import" {
		t.Fatalf("keyed package preview: %+v %v", p, err)
	}
}

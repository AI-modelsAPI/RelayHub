package export

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/secrets"
	"relayhub/internal/storage"
)

func TestExportAndImportWorkflow(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	repo := repository.New(db.DB)
	ctx := context.Background()

	// Seed source data
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p1", Name: "Provider1", AdapterType: "generic", Enabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c1", ProviderID: "p1", Name: "Channel1", BaseURL: "http://example.com", Enabled: true})
	_ = repo.CreateModel(ctx, domain.Model{ID: "m1", DisplayName: "Model1", Enabled: true})
	_ = repo.CreateRoute(ctx, domain.Route{ID: "r1", Name: "Route1", Protocol: "openai", ModelPattern: "*", Strategy: "priority", Enabled: true})

	// 1. Export non-sensitive
	pkgBytes, err := Create(ctx, repo, ExportOptions{IncludeSecrets: false})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Preview package
	preview, err := PreviewPackage(ctx, pkgBytes, repo)
	if err != nil {
		t.Fatalf("Preview failed: %v", err)
	}
	if preview.TotalEntities != 4 {
		t.Fatalf("expected 4 entities, got %d", preview.TotalEntities)
	}
	if len(preview.Conflicts) != 2 { // p1 and c1 exist
		t.Fatalf("expected 2 conflicts, got %d", len(preview.Conflicts))
	}

	// 3. Import to clean database
	db2, err := storage.Open(filepath.Join(t.TempDir(), "test2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	_ = db2.Migrate(context.Background())
	repo2 := repository.New(db2.DB)

	err = Apply(ctx, pkgBytes, *repo2, ConflictSkip, "")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Verify imported records exist
	p, err := repo2.GetProvider(ctx, "p1")
	if err != nil || p.Name != "Provider1" {
		t.Fatalf("expected Provider1 imported, got %+v, err=%v", p, err)
	}
	m, err := repo2.GetModel(ctx, "m1")
	if err != nil || m.DisplayName != "Model1" {
		t.Fatalf("expected Model1 imported, got %+v, err=%v", m, err)
	}
}

func TestEncryptedExportWrongPasswordRejection(t *testing.T) {
	data := []byte("confidential-tokens-and-keys")
	password := "Correct-Horse-Battery-Staple-2026"

	enc, err := EncryptPayload(data, password)
	if err != nil {
		t.Fatalf("EncryptPayload failed: %v", err)
	}

	// Correct password decrypts
	dec, err := DecryptPayload(enc, password)
	if err != nil || string(dec) != string(data) {
		t.Fatalf("decryption failed: %v", err)
	}

	// Wrong password rejected
	_, err = DecryptPayload(enc, "Wrong-Password")
	if err != ErrDecryptionFailed {
		t.Fatalf("expected ErrDecryptionFailed, got %v", err)
	}

	// Tampered data rejected
	tampered := append([]byte(nil), enc...)
	tampered[len(tampered)-1] ^= 0x55
	_, err = DecryptPayload(tampered, password)
	if err != ErrDecryptionFailed {
		t.Fatalf("expected tamper error, got %v", err)
	}
}

// TestTamperedChecksumRejection verifies that corrupted or forged checksums are rejected by PreviewPackage and Apply
func TestTamperedChecksumRejection(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_ = db.Migrate(context.Background())
	repo := repository.New(db.DB)
	ctx := context.Background()

	// Tamper: bogus checksum
	tampered := []byte(`{"manifest":{"version":1,"created_at":"2026-09-14T00:00:00Z","has_secrets":false,"checksum":"bogus-checksum"},"providers":[],"channels":[],"models":[],"routes":[]}`)

	// Preview should fail closed on tampered checksum
	_, err = PreviewPackage(ctx, tampered, repo)
	if err == nil {
		t.Fatalf("PROBE15 PreviewPackage(bogus checksum) -> err=<nil>, expected error")
	}

	// Apply should also fail closed
	err = Apply(ctx, tampered, *repo, ConflictSkip, "")
	if err == nil {
		t.Fatalf("Apply(bogus checksum) -> err=<nil>, expected error")
	}
}

// TestImportOverwriteErrorRollsBack verifies that errors during ConflictOverwrite trigger a rollback
func TestImportOverwriteErrorRollsBack(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_ = db.Migrate(context.Background())
	repo := repository.New(db.DB)
	ctx := context.Background()

	// Seed existing channel c1
	err = repo.CreateProvider(ctx, domain.Provider{
		ID:          "p1",
		Name:        "OriginalProvider",
		AdapterType: "generic",
		Enabled:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = repo.CreateChannel(ctx, domain.Channel{
		ID:         "c1",
		ProviderID: "p1",
		Name:       "ChannelOriginal",
		BaseURL:    "http://example.com",
		Enabled:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Enable foreign keys explicitly in SQLite
	if _, err := db.DB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	// Package where provider p2 is created, but channel c1 update violates foreign key
	pkg := PackageData{
		Manifest: Manifest{
			Version: 1,
		},
		Providers: []domain.Provider{
			{ID: "p2", Name: "ProviderTwo", AdapterType: "generic", Enabled: true},
		},
		Channels: []domain.Channel{
			{ID: "c1", ProviderID: "non-existent-p", Name: "ChannelUpdated", BaseURL: "http://example.com", Enabled: true},
		},
	}

	checksum, err := ComputePackageChecksum(pkg)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Manifest.Checksum = checksum

	pkgBytes, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}

	err = Apply(ctx, pkgBytes, *repo, ConflictOverwrite, "")
	if err == nil {
		p2, getErr := repo.GetProvider(ctx, "p2")
		if getErr == nil && p2.ID == "p2" {
			t.Fatalf("Apply swallowed UpdateChannel error: transaction committed invalid state, p2 was created (expected rollback)")
		}
		t.Fatalf("expected Apply to return error on UpdateChannel failure, got nil")
	}

	// Verify rollback: p2 must not exist
	_, err = repo.GetProvider(ctx, "p2")
	if err == nil {
		t.Fatalf("transaction was not rolled back: p2 exists in repo")
	}
}

// TestRealSecretExportAndImportRoundTrip verifies that encrypted exports carry real secrets and restore them
func TestRealSecretExportAndImportRoundTrip(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_ = db.Migrate(context.Background())
	repo := repository.New(db.DB)
	ctx := context.Background()

	// Setup secret store
	keyProvider, _ := secrets.NewStaticKeyProvider(make([]byte, 32))
	secStore, err := secrets.NewProductionStore(keyProvider, db.DB)
	if err != nil {
		t.Fatal(err)
	}

	// Put actual secret into secret store
	credRef := "channel-cred-123"
	secretVal := "sk-real-api-key-secret-value-999"
	err = secStore.Put(ctx, credRef, []byte(secretVal))
	if err != nil {
		t.Fatal(err)
	}

	// Create channel with CredentialRef
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p1", Name: "Provider1", AdapterType: "generic", Enabled: true})
	_ = repo.CreateChannel(ctx, domain.Channel{
		ID:            "c1",
		ProviderID:    "p1",
		Name:          "Channel1",
		BaseURL:       "http://example.com",
		CredentialRef: credRef,
		Enabled:       true,
	})

	exportPass := "strong-export-password-123"
	pkgBytes, err := Create(ctx, repo, ExportOptions{
		IncludeSecrets: true,
		Password:       exportPass,
		SecretExporter: secStore.ListAll,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify the decrypted payload is NOT placeholder
	var pkg PackageData
	if err := json.Unmarshal(pkgBytes, &pkg); err != nil {
		t.Fatal(err)
	}
	rawEnc, err := base64.StdEncoding.DecodeString(pkg.Secrets)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := DecryptPayload(rawEnc, exportPass)
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) == `{"note":"encrypted credentials placeholder"}` {
		t.Fatalf("PROBE16 decrypted secret section = %s", string(decrypted))
	}

	var secretMap map[string][]byte
	if err := json.Unmarshal(decrypted, &secretMap); err != nil {
		t.Fatalf("unmarshal decrypted secret map: %v", err)
	}
	if string(secretMap[credRef]) != secretVal {
		t.Fatalf("expected secret %q, got %q", secretVal, string(secretMap[credRef]))
	}

	// Setup clean target database
	db2, err := storage.Open(filepath.Join(t.TempDir(), "test2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	_ = db2.Migrate(ctx)
	repo2 := repository.New(db2.DB)
	secStore2, err := secrets.NewProductionStore(keyProvider, db2.DB)
	if err != nil {
		t.Fatal(err)
	}

	// Apply with secret importer
	err = ApplyWithOptions(ctx, pkgBytes, *repo2, ApplyOptions{
		Policy:   ConflictSkip,
		Password: exportPass,
		SecretImporter: func(ctx context.Context, importedSecrets map[string][]byte) error {
			for ref, val := range importedSecrets {
				if err := secStore2.Put(ctx, ref, val); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ApplyWithOptions failed: %v", err)
	}

	// Verify imported channel and credential
	ch, err := repo2.GetChannel(ctx, "c1")
	if err != nil || ch.CredentialRef != credRef {
		t.Fatalf("expected channel c1 imported with credRef %s, got %+v, err=%v", credRef, ch, err)
	}

	restoredVal, err := secStore2.Get(ctx, credRef)
	if err != nil {
		t.Fatalf("restored secret not found in secStore2: %v", err)
	}
	if string(restoredVal) != secretVal {
		t.Fatalf("restored secret mismatch: expected %q, got %q", secretVal, string(restoredVal))
	}
}

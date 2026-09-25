package export

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
)

func PreviewPackage(ctx context.Context, packageBytes []byte, repo repository.ResourceRepository) (Preview, error) {
	var pkg PackageData
	if err := json.Unmarshal(packageBytes, &pkg); err != nil {
		return Preview{}, fmt.Errorf("invalid package format: %w", err)
	}

	if pkg.Manifest.Version <= 0 || pkg.Manifest.Version > CurrentPackageVersion {
		return Preview{}, fmt.Errorf("unsupported package version: %d", pkg.Manifest.Version)
	}
	if pkg.Manifest.Checksum == "" {
		return Preview{}, ErrTamperedPackage
	}
	// version>=2 packages carry an HMAC keyed with the export password; the
	// caller previewing without a password cannot fully validate them, so the
	// preview reports integrity as "pending" and application re-checks with
	// the password (AUDIT RH-13).
	if pkg.Manifest.Version == 1 {
		expectedChecksum, err := ComputePackageChecksum(pkg)
		if err != nil {
			return Preview{}, fmt.Errorf("failed to compute package checksum: %w", err)
		}
		if pkg.Manifest.Checksum != expectedChecksum {
			return Preview{}, ErrTamperedPackage
		}
	}

	existingProviders, _ := repo.ListProviders(ctx)
	pMap := make(map[string]bool)
	for _, p := range existingProviders {
		pMap[p.ID] = true
	}

	existingChannels, _ := repo.ListChannels(ctx, "")
	cMap := make(map[string]bool)
	for _, c := range existingChannels {
		cMap[c.ID] = true
	}

	var conflicts []ConflictItem

	// Decode providers to inspect conflicts
	rawP, _ := json.Marshal(pkg.Providers)
	var provs []domain.Provider
	_ = json.Unmarshal(rawP, &provs)
	for _, p := range provs {
		if pMap[p.ID] {
			conflicts = append(conflicts, ConflictItem{Type: "provider", ID: p.ID, Name: p.Name})
		}
	}

	rawC, _ := json.Marshal(pkg.Channels)
	var chans []domain.Channel
	_ = json.Unmarshal(rawC, &chans)
	for _, c := range chans {
		if cMap[c.ID] {
			conflicts = append(conflicts, ConflictItem{Type: "channel", ID: c.ID, Name: c.Name})
		}
	}

	total := pkg.Manifest.ProviderCount + pkg.Manifest.ChannelCount + pkg.Manifest.ModelCount + pkg.Manifest.RouteCount + pkg.Manifest.ProviderModelCount + pkg.Manifest.ModelGroupCount + pkg.Manifest.ChannelKeyCount

	return Preview{
		Version:       pkg.Manifest.Version,
		Integrity:     integrityLabel(pkg.Manifest),
		CreatedAt:     pkg.Manifest.CreatedAt,
		HasSecrets:    pkg.Manifest.HasSecrets,
		TotalEntities: total,
		Conflicts:     conflicts,
	}, nil
}

type ApplyOptions struct {
	Policy         ConflictPolicy
	Password       string
	SecretImporter SecretImporter
}

func Apply(ctx context.Context, packageBytes []byte, repo repository.Store, policy ConflictPolicy, password string) error {
	return ApplyWithOptions(ctx, packageBytes, repo, ApplyOptions{
		Policy:   policy,
		Password: password,
	})
}

func ApplyWithOptions(ctx context.Context, packageBytes []byte, repo repository.Store, opts ApplyOptions) error {
	var pkg PackageData
	if err := json.Unmarshal(packageBytes, &pkg); err != nil {
		return err
	}

	if pkg.Manifest.Version <= 0 || pkg.Manifest.Version > CurrentPackageVersion {
		return fmt.Errorf("unsupported package version: %d", pkg.Manifest.Version)
	}
	if pkg.Manifest.Checksum == "" {
		return ErrTamperedPackage
	}

	if pkg.Manifest.HasSecrets && opts.Password == "" {
		return errors.New("password required to import encrypted package")
	}
	// Sanity: v2 manifests advertise counts for the new tables; refuse forks
	// that claim v2 but omit the arrays entirely (tampering heuristics).
	if pkg.Manifest.Version >= 2 && (pkg.Manifest.ProviderModelCount > 0) && len(pkg.ProviderModels) == 0 {
		return errors.New("v2 manifest lists provider bindings but the package carries none")
	}
	// v2 HMAC covers the entire package with the password-derived key; v1 is
	// legacy plain SHA-256 and is only accepted for packages without secrets.
	if pkg.Manifest.Version >= 2 || pkg.Manifest.HasSecrets {
		if opts.Password == "" {
			return errors.New("password required to verify package integrity")
		}
		compute := ComputePackageChecksumV2
		if pkg.Manifest.Version >= 3 {
			compute = ComputePackageChecksumV3
		}
		expectedChecksum, err := compute(pkg, opts.Password)
		if err != nil {
			return fmt.Errorf("failed to compute package checksum: %w", err)
		}
		if !hmac.Equal([]byte(pkg.Manifest.Checksum), []byte(expectedChecksum)) {
			return ErrTamperedPackage
		}
	} else {
		expectedChecksum, err := ComputePackageChecksum(pkg)
		if err != nil {
			return fmt.Errorf("failed to compute package checksum: %w", err)
		}
		if pkg.Manifest.Checksum != expectedChecksum {
			return ErrTamperedPackage
		}
	}

	var decryptedSecrets map[string][]byte
	if pkg.Manifest.HasSecrets && pkg.Secrets != "" {
		enc, err := base64.StdEncoding.DecodeString(pkg.Secrets)
		if err != nil {
			return fmt.Errorf("invalid encrypted secret encoding: %w", err)
		}
		plain, err := DecryptPayload(enc, opts.Password)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(plain, &decryptedSecrets); err != nil {
			return fmt.Errorf("invalid secret payload: %w", err)
		}
	}

	rawP, _ := json.Marshal(pkg.Providers)
	var provs []domain.Provider
	if err := json.Unmarshal(rawP, &provs); err != nil {
		return err
	}

	rawC, _ := json.Marshal(pkg.Channels)
	var chans []domain.Channel
	if err := json.Unmarshal(rawC, &chans); err != nil {
		return err
	}

	rawM, _ := json.Marshal(pkg.Models)
	var models []domain.Model
	if err := json.Unmarshal(rawM, &models); err != nil {
		return err
	}

	rawR, _ := json.Marshal(pkg.Routes)
	var routes []domain.Route
	if err := json.Unmarshal(rawR, &routes); err != nil {
		return err
	}

	// v2 additions: bindings/groups/keys migrate with the package so a
	// re-imported deployment is functionally identical (AUDIT RH-13).
	rawPM, _ := json.Marshal(pkg.ProviderModels)
	var pms []domain.ProviderModel
	if err := json.Unmarshal(rawPM, &pms); err != nil {
		return err
	}
	rawMG, _ := json.Marshal(pkg.ModelGroups)
	var groups []domain.ModelGroup
	if err := json.Unmarshal(rawMG, &groups); err != nil {
		return err
	}
	rawMM, _ := json.Marshal(pkg.ModelGroupMembers)
	var members []domain.ModelGroupMember
	if err := json.Unmarshal(rawMM, &members); err != nil {
		return err
	}
	rawCK, _ := json.Marshal(pkg.ChannelKeys)
	var chKeys []domain.ChannelKey
	if err := json.Unmarshal(rawCK, &chKeys); err != nil {
		return err
	}

	// Run inside atomic transaction
	err := repo.WithTx(ctx, func(tx *repository.Tx) error {
		for _, p := range provs {
			existing, err := tx.GetProvider(ctx, p.ID)
			if err == nil && existing.ID != "" {
				if opts.Policy == ConflictOverwrite {
					if err := tx.UpdateProvider(ctx, p); err != nil {
						return fmt.Errorf("update provider %s: %w", p.ID, err)
					}
				}
				continue
			}
			if err := tx.CreateProvider(ctx, p); err != nil {
				return fmt.Errorf("create provider %s: %w", p.ID, err)
			}
		}

		for _, m := range models {
			existing, err := tx.GetModel(ctx, m.ID)
			if err == nil && existing.ID != "" {
				if opts.Policy == ConflictOverwrite {
					if err := tx.UpdateModel(ctx, m); err != nil {
						return fmt.Errorf("update model %s: %w", m.ID, err)
					}
				}
				continue
			}
			if err := tx.CreateModel(ctx, m); err != nil {
				return fmt.Errorf("create model %s: %w", m.ID, err)
			}
		}

		for _, c := range chans {
			existing, err := tx.GetChannel(ctx, c.ID)
			if err == nil && existing.ID != "" {
				if opts.Policy == ConflictOverwrite {
					if err := tx.UpdateChannel(ctx, c); err != nil {
						return fmt.Errorf("update channel %s: %w", c.ID, err)
					}
				}
				continue
			}
			if err := tx.CreateChannel(ctx, c); err != nil {
				return fmt.Errorf("create channel %s: %w", c.ID, err)
			}
		}

		for _, r := range routes {
			existing, err := tx.GetRoute(ctx, r.ID)
			if err == nil && existing.ID != "" {
				if opts.Policy == ConflictOverwrite {
					if err := tx.UpdateRoute(ctx, r); err != nil {
						return fmt.Errorf("update route %s: %w", r.ID, err)
					}
				}
				continue
			}
			if err := tx.CreateRoute(ctx, r); err != nil {
				return fmt.Errorf("create route %s: %w", r.ID, err)
			}
		}

		for _, pm := range pms {
			existing, err := tx.GetProviderModel(ctx, pm.ID)
			if err == nil && existing.ID != "" {
				if opts.Policy == ConflictOverwrite {
					if err := tx.UpdateProviderModel(ctx, pm); err != nil {
						return fmt.Errorf("update provider model %s: %w", pm.ID, err)
					}
				}
				continue
			}
			if err := tx.CreateProviderModel(ctx, pm); err != nil {
				return fmt.Errorf("create provider model %s: %w", pm.ID, err)
			}
		}

		for _, g := range groups {
			existing, err := tx.GetModelGroup(ctx, g.ID)
			if err == nil && existing.ID != "" {
				if opts.Policy == ConflictOverwrite {
					if err := tx.UpdateModelGroup(ctx, g); err != nil {
						return fmt.Errorf("update model group %s: %w", g.ID, err)
					}
				}
				continue
			}
			if err := tx.CreateModelGroup(ctx, g); err != nil {
				return fmt.Errorf("create model group %s: %w", g.ID, err)
			}
		}

		for _, m := range members {
			if err := tx.AddModelGroupMember(ctx, m); err != nil {
				if opts.Policy == ConflictOverwrite {
					if rerr := tx.RemoveModelGroupMember(ctx, m.GroupID, m.ModelID); rerr != nil {
						return fmt.Errorf("re-add model group member %s/%s: %w", m.GroupID, m.ModelID, rerr)
					}
					if err := tx.AddModelGroupMember(ctx, m); err != nil {
						return fmt.Errorf("re-add model group member %s/%s: %w", m.GroupID, m.ModelID, err)
					}
					continue
				}
				// skip policy: a duplicate member row is not a fatal import error
				continue
			}
		}

		for _, k := range chKeys {
			existing, err := tx.GetChannelKey(ctx, k.ID)
			if err == nil && existing.ID != "" {
				if opts.Policy == ConflictOverwrite {
					if err := tx.UpdateChannelKey(ctx, k); err != nil {
						return fmt.Errorf("update channel key %s: %w", k.ID, err)
					}
				}
				continue
			}
			if err := tx.CreateChannelKey(ctx, k); err != nil {
				return fmt.Errorf("create channel key %s: %w", k.ID, err)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if opts.SecretImporter != nil && len(decryptedSecrets) > 0 {
		if err := opts.SecretImporter(ctx, decryptedSecrets); err != nil {
			return err
		}
	}

	return nil
}

func integrityLabel(m Manifest) string {
	if m.Version >= 2 || m.HasSecrets {
		return "verified_on_import"
	}
	return "unauthenticated"
}

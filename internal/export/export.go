package export

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
)

type SecretExporter func(ctx context.Context) (map[string][]byte, error)

type ExportOptions struct {
	IncludeSecrets bool
	Password       string
	SecretExporter SecretExporter
}

func Create(ctx context.Context, repo repository.ResourceRepository, opts ExportOptions) ([]byte, error) {
	providers, err := repo.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	channels, err := repo.ListChannels(ctx, "")
	if err != nil {
		return nil, err
	}
	models, err := repo.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	routes, err := repo.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	// Complete configuration coverage: bindings, groups (with members) and
	// channel-key metadata were previously dropped from exports (AUDIT RH-13).
	providerModels, err := repo.ListProviderModels(ctx, "")
	if err != nil {
		return nil, err
	}
	modelGroups, err := repo.ListModelGroups(ctx)
	if err != nil {
		return nil, err
	}
	var groupMembers []domain.ModelGroupMember
	for _, g := range modelGroups {
		members, merr := repo.ListModelGroupMembers(ctx, g.ID)
		if merr != nil {
			return nil, merr
		}
		groupMembers = append(groupMembers, members...)
	}
	var channelKeys []domain.ChannelKey
	for _, ch := range channels {
		keys, kerr := repo.ListChannelKeys(ctx, ch.ID)
		if kerr != nil {
			return nil, kerr
		}
		channelKeys = append(channelKeys, keys...)
	}

	creds := opts.IncludeSecrets && opts.Password != ""
	version := 1
	if creds {
		version = CurrentPackageVersion
	}
	manifest := Manifest{
		Version:            version,
		CreatedAt:          time.Now().UTC(),
		HasSecrets:         creds,
		ProviderCount:      len(providers),
		ChannelCount:       len(channels),
		ModelCount:         len(models),
		RouteCount:         len(routes),
		ProviderModelCount: len(providerModels),
		ModelGroupCount:    len(modelGroups),
		ChannelKeyCount:    len(channelKeys),
	}

	pkg := PackageData{
		Manifest:          manifest,
		Providers:         providers,
		Channels:          channels,
		Models:            models,
		Routes:            routes,
		ProviderModels:    providerModels,
		ModelGroups:       modelGroups,
		ModelGroupMembers: groupMembers,
		ChannelKeys:       channelKeys,
	}

	// Optional encrypted secret section
	if creds {
		var secMap map[string][]byte
		if opts.SecretExporter != nil {
			m, err := opts.SecretExporter(ctx)
			if err != nil {
				return nil, err
			}
			secMap = m
		} else {
			secMap = make(map[string][]byte)
		}

		secretPayload, err := json.Marshal(secMap)
		if err != nil {
			return nil, err
		}
		enc, err := EncryptPayload(secretPayload, opts.Password)
		if err != nil {
			return nil, err
		}
		pkg.Secrets = base64.StdEncoding.EncodeToString(enc)
	}

	// v2 (password-protected) packages get a keyed HMAC checksum so nobody can
	// edit the payload and recompute the integrity value; legacy v1 packages
	// keep the plain SHA-256 (see manifest docs, AUDIT RH-13).
	var checksum string
	if creds {
		checksum, err = ComputePackageChecksumV2(pkg, opts.Password)
	} else {
		checksum, err = ComputePackageChecksum(pkg)
	}
	if err != nil {
		return nil, err
	}
	pkg.Manifest.Checksum = checksum

	return json.MarshalIndent(pkg, "", "  ")
}

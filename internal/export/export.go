package export

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

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

	manifest := Manifest{
		Version:       CurrentPackageVersion,
		CreatedAt:     time.Now().UTC(),
		HasSecrets:    opts.IncludeSecrets,
		ProviderCount: len(providers),
		ChannelCount:  len(channels),
		ModelCount:    len(models),
		RouteCount:    len(routes),
	}

	pkg := PackageData{
		Manifest:  manifest,
		Providers: providers,
		Channels:  channels,
		Models:    models,
		Routes:    routes,
	}

	// Optional encrypted secret section
	if opts.IncludeSecrets && opts.Password != "" {
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

	checksum, err := ComputePackageChecksum(pkg)
	if err != nil {
		return nil, err
	}
	pkg.Manifest.Checksum = checksum

	return json.MarshalIndent(pkg, "", "  ")
}

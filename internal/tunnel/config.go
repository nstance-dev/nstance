// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

const configKey = "config.jsonc"

// BootstrapConfig identifies the shard configuration read by the tunnel command.
type BootstrapConfig struct {
	Storage string
	Bucket  string
	Shard   string
	Prefix  string
}

// Config contains independently loaded tunnel configuration and providers.
type Config struct {
	Tunnels map[string]config.TunnelPodConfig
	Storage storage.Storage
	Secrets secrets.Store
}

// LoadConfig independently loads the root-controlled shard configuration.
func LoadConfig(ctx context.Context, logger *slog.Logger, bootstrap BootstrapConfig) (*Config, func(), error) {
	if bootstrap.Storage == "" || bootstrap.Bucket == "" || bootstrap.Shard == "" {
		return nil, nil, fmt.Errorf("storage, bucket, and shard are required")
	}
	base, cleanup, err := storage.New(ctx, logger, bootstrap.Storage, bootstrap.Bucket)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*Config, func(), error) {
		cleanup()
		return nil, nil, err
	}
	prefix := bootstrap.Prefix
	if prefix == "" {
		prefix = "shard/" + bootstrap.Shard + "/"
	}
	shardStorage := storage.NewScopedStorage(base, prefix)
	data, _, err := shardStorage.Get(ctx, configKey)
	if err != nil {
		return fail(fmt.Errorf("read shard configuration: %w", err))
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return fail(fmt.Errorf("parse shard configuration: %w", err))
	}
	if cfg.Shard.ID != bootstrap.Shard {
		return fail(fmt.Errorf("configuration shard %q does not match %q", cfg.Shard.ID, bootstrap.Shard))
	}

	clusterStorage := storage.NewScopedStorage(base, "cluster/")
	clusterCleanup := func() {}
	if cfg.Cluster.Storage != nil && cfg.Cluster.Storage.Bucket != "" {
		clusterStorage, clusterCleanup, err = storage.NewWithOptions(ctx, logger, storage.StorageOptions{
			Provider: cfg.Cluster.Storage.Provider,
			Bucket:   cfg.Cluster.Storage.Bucket,
			Region:   cfg.Cluster.Storage.Region,
			Endpoint: cfg.Cluster.Storage.Endpoint,
		})
		if err != nil {
			return fail(fmt.Errorf("create cluster storage: %w", err))
		}
		clusterStorage = storage.NewScopedStorage(clusterStorage, cfg.Cluster.Storage.Prefix)
	}
	storeOptions := secrets.StoreOptions{
		Provider: cfg.Cluster.Secrets.Provider, Prefix: cfg.Cluster.Secrets.Prefix,
		CacheTTL: cfg.Cluster.Secrets.CacheTTL.Duration(), ProjectID: cfg.Cluster.Secrets.ProjectID,
		Storage: clusterStorage,
	}
	if key := cfg.Cluster.Secrets.EncryptionKey; key != nil {
		storeOptions.EncryptionKeys = append(storeOptions.EncryptionKeys, secrets.KeyConfig{Provider: key.Provider, ProjectID: key.ProjectID, Source: key.Source})
	}
	for _, key := range cfg.Cluster.Secrets.OldEncryptionKeys {
		storeOptions.EncryptionKeys = append(storeOptions.EncryptionKeys, secrets.KeyConfig{Provider: key.Provider, ProjectID: key.ProjectID, Source: key.Source})
	}
	secretStore, err := secrets.NewStore(ctx, storeOptions)
	if err != nil {
		clusterCleanup()
		return fail(fmt.Errorf("create secrets store: %w", err))
	}
	return &Config{Tunnels: cfg.Tunnels, Storage: shardStorage, Secrets: secretStore}, func() {
		clusterCleanup()
		cleanup()
	}, nil
}

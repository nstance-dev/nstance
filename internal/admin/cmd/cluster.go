// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Operations requiring direct access to cluster storage and secrets",
	Long: `Commands that operate directly on cluster storage and secrets.

These commands bypass nstance-servers and access storage/secrets directly.
Use these for cluster bootstrap operations or when leader election is disabled.`,
	PersistentPreRunE: initClusterServices,
}

var (
	flagClusterStorageProvider   string
	flagClusterStorageBucket     string
	flagClusterStoragePrefix     string
	flagClusterSecretsProvider   string
	flagClusterSecretsPrefix     string
	flagClusterSecretsGCPProject string
	flagClusterKeyProvider       string
	flagClusterKeySource         string
)

var (
	clusterStorage      storage.Storage
	clusterSecretsStore secrets.Store
)

func init() {
	pflags := clusterCmd.PersistentFlags()
	pflags.StringVar(&flagClusterStorageProvider, "storage-provider", "s3", "Storage provider (s3, gcs, file)")
	pflags.StringVar(&flagClusterStorageBucket, "storage-bucket", "", "Storage bucket name")
	pflags.StringVar(&flagClusterStoragePrefix, "storage-prefix", "cluster/", "Storage prefix for cluster data")
	pflags.StringVar(&flagClusterSecretsProvider, "secrets-provider", "object-storage", "Secrets provider (object-storage, aws-secrets-manager, gcp-secret-manager)")
	pflags.StringVar(&flagClusterSecretsPrefix, "secrets-prefix", "secret/", "Secrets prefix")
	pflags.StringVar(&flagClusterSecretsGCPProject, "secrets-gcp-project", "", "GCP project ID (required for gcp-secret-manager provider)")
	pflags.StringVar(&flagClusterKeyProvider, "key-provider", "", "Encryption key provider (env, file, aws-secrets-manager, gcp-secret-manager) - required for object-storage secrets provider")
	pflags.StringVar(&flagClusterKeySource, "key-source", "", "Encryption key source (env var name, file path, or secret ARN) - defaults to NSTANCE_ENCRYPTION_KEY for env provider")

	_ = clusterCmd.MarkPersistentFlagRequired("storage-bucket")

	rootCmd.AddCommand(clusterCmd)
}

func initClusterServices(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	logger := getLogger()

	rawStorage, cleanup, err := storage.New(ctx, logger, flagClusterStorageProvider, flagClusterStorageBucket)
	if err != nil {
		return fmt.Errorf("failed to create cluster storage: %w", err)
	}
	// Store cleanup function - will be called when command finishes
	cmd.PostRunE = func(_ *cobra.Command, _ []string) error {
		cleanup()
		return nil
	}

	prefix := flagClusterStoragePrefix
	if prefix == "" {
		prefix = "cluster/"
	}
	clusterStorage = storage.NewScopedStorage(rawStorage, prefix)

	secretsStore, err := initClusterSecretsStore(ctx)
	if err != nil {
		cleanup()
		return err
	}
	clusterSecretsStore = secretsStore

	return nil
}

func initClusterSecretsStore(ctx context.Context) (secrets.Store, error) {
	storeOpts := secrets.StoreOptions{
		Provider:   flagClusterSecretsProvider,
		Prefix:     flagClusterSecretsPrefix,
		GCPProject: flagClusterSecretsGCPProject,
	}

	switch flagClusterSecretsProvider {
	case "object-storage":
		if flagClusterKeyProvider == "" {
			return nil, fmt.Errorf("--key-provider is required for object-storage secrets provider")
		}
		keySource := flagClusterKeySource
		if keySource == "" {
			if flagClusterKeyProvider == "env" {
				keySource = "NSTANCE_ENCRYPTION_KEY"
			} else {
				return nil, fmt.Errorf("--key-source is required for %s key provider", flagClusterKeyProvider)
			}
		}
		storeOpts.Storage = clusterStorage
		keyCfg := secrets.KeyConfig{
			Provider: flagClusterKeyProvider,
			Source:   keySource,
		}
		if flagClusterKeyProvider == "gcp-secret-manager" {
			if flagClusterSecretsGCPProject == "" {
				return nil, fmt.Errorf("--secrets-gcp-project is required for gcp-secret-manager key provider")
			}
			keyCfg.Options = map[string]interface{}{"project_id": flagClusterSecretsGCPProject}
		}
		storeOpts.EncryptionKeys = []secrets.KeyConfig{keyCfg}
	case "gcp-secret-manager":
		if storeOpts.GCPProject == "" {
			return nil, fmt.Errorf("--secrets-gcp-project is required for gcp-secret-manager provider")
		}
	case "aws-secrets-manager":
	default:
		return nil, fmt.Errorf("unsupported secrets provider: %s", flagClusterSecretsProvider)
	}

	return secrets.NewStore(ctx, storeOpts)
}

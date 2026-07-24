// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
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
	flagClusterStorageProvider string
	flagClusterStorageBucket   string
	flagClusterStoragePrefix   string
	flagClusterSecretsProvider string
	flagClusterSecretsPrefix   string
	flagClusterSecretsProject  string
	flagClusterKeyProvider     string
	flagClusterKeySource       string
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
	pflags.StringVar(&flagClusterSecretsProvider, "secrets-provider", "", "Secrets provider (object-storage, aws-parameter-store, aws-secrets-manager, google-secret-manager)")
	pflags.StringVar(&flagClusterSecretsPrefix, "secrets-prefix", "secret/", "Secrets prefix")
	pflags.StringVar(&flagClusterSecretsProject, "secrets-project", "", "Project ID (required for google-secret-manager provider)")
	pflags.StringVar(&flagClusterKeyProvider, "key-provider", "", "Encryption key provider (env, file, aws-parameter-store, aws-secrets-manager, google-secret-manager) - required for object-storage secrets provider")
	pflags.StringVar(&flagClusterKeySource, "key-source", "", "Encryption key source (env var name, file path, parameter name, or secret ARN) - defaults to NSTANCE_ENCRYPTION_KEY for env provider")

	_ = clusterCmd.MarkPersistentFlagRequired("storage-bucket")
	_ = clusterCmd.MarkPersistentFlagRequired("secrets-provider")

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
		Provider:  flagClusterSecretsProvider,
		Prefix:    flagClusterSecretsPrefix,
		ProjectID: flagClusterSecretsProject,
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
			Provider:  flagClusterKeyProvider,
			ProjectID: flagClusterSecretsProject,
			Source:    keySource,
		}
		if flagClusterKeyProvider == "google-secret-manager" {
			if flagClusterSecretsProject == "" {
				return nil, fmt.Errorf("--secrets-project is required for google-secret-manager key provider")
			}
		}
		storeOpts.EncryptionKeys = []secrets.KeyConfig{keyCfg}
	case "google-secret-manager":
		if storeOpts.ProjectID == "" {
			return nil, fmt.Errorf("--secrets-project is required for google-secret-manager provider")
		}
	case "aws-parameter-store", "aws-secrets-manager":
	default:
		return nil, fmt.Errorf("unsupported secrets provider: %s", flagClusterSecretsProvider)
	}

	return secrets.NewStore(ctx, storeOpts)
}

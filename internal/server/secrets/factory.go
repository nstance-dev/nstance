// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"fmt"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// StoreOptions contains configuration for creating a secrets Store
type StoreOptions struct {
	Provider       string
	Prefix         string
	CacheTTL       time.Duration
	EncryptionKeys []KeyConfig // Only used for object-storage provider
	ProjectID      string      // Required for google-secret-manager provider
	Storage        storage.Storage
}

// NewStore creates a new secrets Store based on options.
// Creates cloud clients internally based on Provider.
// Loads encryption keys only for the object-storage provider.
func NewStore(ctx context.Context, opts StoreOptions) (Store, error) {
	var store Store

	switch opts.Provider {
	case "object-storage":
		if opts.Storage == nil {
			return nil, fmt.Errorf("storage is required for object-storage provider")
		}
		keys, err := LoadEncryptionKeys(ctx, opts.EncryptionKeys...)
		if err != nil {
			return nil, fmt.Errorf("load encryption keys: %w", err)
		}
		store = NewObjectStorageStore(opts.Storage, opts.Prefix, keys)

	case "aws-secrets-manager":
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		client := secretsmanager.NewFromConfig(awsCfg)
		store = NewAWSSecretsManagerStore(client, opts.Prefix)

	case "aws-parameter-store":
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		client := ssm.NewFromConfig(awsCfg)
		store = NewAWSParameterStore(client, opts.Prefix)

	case "google-secret-manager":
		if opts.ProjectID == "" {
			return nil, fmt.Errorf("project_id is required for google-secret-manager provider")
		}
		client, err := secretmanager.NewClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("create Google Cloud Secret Manager client: %w", err)
		}
		store = NewGoogleSecretManagerStore(client, opts.ProjectID, opts.Prefix)

	case "memory":
		store = NewMemoryStore()

	default:
		return nil, fmt.Errorf("unsupported secrets provider: %s", opts.Provider)
	}

	if opts.CacheTTL > 0 {
		store = NewCachedStore(store, opts.CacheTTL)
	}

	return store, nil
}

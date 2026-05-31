// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// KeyConfig defines configuration for loading a single encryption key
type KeyConfig struct {
	Provider string
	Options  map[string]interface{}
	Source   string
}

// LoadEncryptionKeys loads all encryption keys from the given configs.
// Note that the first key will be the current encryption key, and any
// additional keys will be old keys used only for decryption.
func LoadEncryptionKeys(ctx context.Context, keys ...KeyConfig) ([][]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	// Create required clients
	var awsClient *secretsmanager.Client
	var gcpClient *secretmanager.Client
	for _, k := range keys {
		switch k.Provider {
		case "aws-secrets-manager":
			awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
			if err != nil {
				return nil, fmt.Errorf("load AWS config: %w", err)
			}
			awsClient = secretsmanager.NewFromConfig(awsCfg)
		case "gcp-secret-manager":
			client, err := secretmanager.NewClient(ctx)
			if err != nil {
				return nil, fmt.Errorf("create GCP Secret Manager client: %w", err)
			}
			gcpClient = client
			defer func() { _ = gcpClient.Close() }()
		}
	}

	// Load each key
	result := make([][]byte, 0, len(keys))
	for _, cfg := range keys {
		key, err := loadKey(ctx, cfg, awsClient, gcpClient)
		if err != nil {
			return nil, err
		}
		result = append(result, key)
	}
	return result, nil
}

func loadKey(ctx context.Context, cfg KeyConfig, awsClient *secretsmanager.Client, gcpClient *secretmanager.Client) ([]byte, error) {
	var key []byte

	switch cfg.Provider {
	case "env":
		val := os.Getenv(cfg.Source)
		if val == "" {
			return nil, fmt.Errorf("environment variable %s not set", cfg.Source)
		}
		key = []byte(val)

	case "file":
		data, err := os.ReadFile(cfg.Source)
		if err != nil {
			return nil, fmt.Errorf("read key file %s: %w", cfg.Source, err)
		}
		key = []byte(strings.TrimSpace(string(data)))

	case "aws-secrets-manager":
		if awsClient == nil {
			return nil, fmt.Errorf("AWS client required for aws-secrets-manager key provider")
		}
		result, err := awsClient.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
			SecretId: aws.String(cfg.Source),
		})
		if err != nil {
			return nil, fmt.Errorf("get secret %s from AWS: %w", cfg.Source, err)
		}
		if result.SecretString != nil {
			key = []byte(*result.SecretString)
		} else if result.SecretBinary != nil {
			key = result.SecretBinary
		} else {
			return nil, fmt.Errorf("AWS secret %s is empty", cfg.Source)
		}

	case "gcp-secret-manager":
		if gcpClient == nil {
			return nil, fmt.Errorf("GCP client required for gcp-secret-manager key provider")
		}
		projectID, ok := cfg.Options["project_id"].(string)
		if !ok || projectID == "" {
			return nil, fmt.Errorf("options.project_id is required for gcp-secret-manager key provider")
		}
		name := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, cfg.Source)
		result, err := gcpClient.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
			Name: name,
		})
		if err != nil {
			return nil, fmt.Errorf("get secret %s from GCP: %w", cfg.Source, err)
		}
		if result.Payload == nil || len(result.Payload.Data) == 0 {
			return nil, fmt.Errorf("GCP secret %s is empty", cfg.Source)
		}
		key = result.Payload.Data

	default:
		return nil, fmt.Errorf("unsupported encryption key provider: %s", cfg.Provider)
	}

	if len(key) != 32 {
		return nil, fmt.Errorf("invalid encryption key length from %s (%s): expected 32 bytes, got %d bytes", cfg.Provider, cfg.Source, len(key))
	}

	return key, nil
}

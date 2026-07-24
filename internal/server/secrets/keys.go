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
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type secretsManagerKeyClient interface {
	GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

type parameterStoreKeyClient interface {
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// KeyConfig defines configuration for loading a single encryption key
type KeyConfig struct {
	Provider  string
	ProjectID string
	Source    string
}

// LoadEncryptionKeys loads all encryption keys from the given configs.
// Note that the first key will be the current encryption key, and any
// additional keys will be old keys used only for decryption.
func LoadEncryptionKeys(ctx context.Context, keys ...KeyConfig) ([][]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	// Create required clients
	var awsSecretsClient secretsManagerKeyClient
	var awsParameterClient parameterStoreKeyClient
	var googleClient *secretmanager.Client
	var needAWSSecrets, needAWSParameters, needGoogle bool
	for _, k := range keys {
		switch k.Provider {
		case "aws-secrets-manager":
			needAWSSecrets = true
		case "aws-parameter-store":
			needAWSParameters = true
		case "google-secret-manager":
			needGoogle = true
		}
	}
	if needAWSSecrets || needAWSParameters {
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		if needAWSSecrets {
			awsSecretsClient = secretsmanager.NewFromConfig(awsCfg)
		}
		if needAWSParameters {
			awsParameterClient = ssm.NewFromConfig(awsCfg)
		}
	}
	if needGoogle {
		client, err := secretmanager.NewClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("create Google Cloud Secret Manager client: %w", err)
		}
		googleClient = client
		defer func() { _ = googleClient.Close() }()
	}

	// Load each key
	result := make([][]byte, 0, len(keys))
	for _, cfg := range keys {
		key, err := loadKey(ctx, cfg, awsSecretsClient, awsParameterClient, googleClient)
		if err != nil {
			return nil, err
		}
		result = append(result, key)
	}
	return result, nil
}

func loadKey(ctx context.Context, cfg KeyConfig, awsSecretsClient secretsManagerKeyClient, awsParameterClient parameterStoreKeyClient, googleClient *secretmanager.Client) ([]byte, error) {
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
		if awsSecretsClient == nil {
			return nil, fmt.Errorf("AWS client required for aws-secrets-manager key provider")
		}
		result, err := awsSecretsClient.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
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

	case "aws-parameter-store":
		if awsParameterClient == nil {
			return nil, fmt.Errorf("AWS client required for aws-parameter-store key provider")
		}
		result, err := awsParameterClient.GetParameter(ctx, &ssm.GetParameterInput{
			Name:           aws.String(cfg.Source),
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			return nil, fmt.Errorf("get parameter %s from AWS: %w", cfg.Source, err)
		}
		if result.Parameter == nil || result.Parameter.Value == nil || *result.Parameter.Value == "" {
			return nil, fmt.Errorf("AWS parameter %s is empty", cfg.Source)
		}
		if result.Parameter.Type != ssmtypes.ParameterTypeSecureString {
			return nil, fmt.Errorf("AWS parameter %s is %s, expected SecureString", cfg.Source, result.Parameter.Type)
		}
		key = []byte(*result.Parameter.Value)

	case "google-secret-manager":
		if googleClient == nil {
			return nil, fmt.Errorf("google-secret-manager key provider requires a client")
		}
		if cfg.ProjectID == "" {
			return nil, fmt.Errorf("project_id is required for google-secret-manager key provider")
		}
		name := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", cfg.ProjectID, cfg.Source)
		result, err := googleClient.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
			Name: name,
		})
		if err != nil {
			return nil, fmt.Errorf("get secret %s from Google Cloud: %w", cfg.Source, err)
		}
		if result.Payload == nil || len(result.Payload.Data) == 0 {
			return nil, fmt.Errorf("secret %s in Google Cloud is empty", cfg.Source)
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

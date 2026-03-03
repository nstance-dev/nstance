// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// AWSSecretsManager implements Store using AWS Secrets Manager
type AWSSecretsManager struct {
	client *secretsmanager.Client
	prefix string
}

// NewAWSSecretsManagerStore creates a new AWS Secrets Manager store
func NewAWSSecretsManagerStore(client *secretsmanager.Client, prefix string) *AWSSecretsManager {
	return &AWSSecretsManager{
		client: client,
		prefix: prefix,
	}
}

// Get retrieves a secret from AWS Secrets Manager
func (a *AWSSecretsManager) Get(ctx context.Context, name string) ([]byte, error) {
	secretName := a.prefix + name

	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	}

	resp, err := a.client.GetSecretValue(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s: %w", name, err)
	}

	if resp.SecretString != nil {
		return []byte(*resp.SecretString), nil
	}

	if resp.SecretBinary != nil {
		return resp.SecretBinary, nil
	}

	return nil, fmt.Errorf("secret %s contains no data", name)
}

// Set stores a secret in AWS Secrets Manager
func (a *AWSSecretsManager) Set(ctx context.Context, name string, data []byte) error {
	secretName := a.prefix + name

	// Try to update existing secret first
	updateInput := &secretsmanager.UpdateSecretInput{
		SecretId:     aws.String(secretName),
		SecretBinary: data,
	}

	_, err := a.client.UpdateSecret(ctx, updateInput)
	if err == nil {
		return nil
	}

	var nf *types.ResourceNotFoundException
	if !errors.As(err, &nf) {
		return fmt.Errorf("failed to update secret %s: %w", name, err)
	}

	// If secret doesn't exist, create it
	createInput := &secretsmanager.CreateSecretInput{
		Name:         aws.String(secretName),
		SecretBinary: data,
		Description:  aws.String(fmt.Sprintf("Nstance secret: %s", name)),
	}

	_, createErr := a.client.CreateSecret(ctx, createInput)
	if createErr != nil {
		return fmt.Errorf("failed to create secret %s: %w", name, createErr)
	}

	return nil
}

// Delete removes a secret from AWS Secrets Manager
func (a *AWSSecretsManager) Delete(ctx context.Context, name string) error {
	secretName := a.prefix + name

	input := &secretsmanager.DeleteSecretInput{
		SecretId:                   aws.String(secretName),
		ForceDeleteWithoutRecovery: aws.Bool(true),
	}

	_, err := a.client.DeleteSecret(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to delete secret %s: %w", name, err)
	}

	return nil
}

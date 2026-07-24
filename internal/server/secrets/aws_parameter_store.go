// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const standardParameterValueLimit = 4 * 1024

type parameterStoreClient interface {
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	PutParameter(context.Context, *ssm.PutParameterInput, ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	DeleteParameter(context.Context, *ssm.DeleteParameterInput, ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}

// AWSParameterStore implements Store using AWS Systems Manager Parameter Store.
type AWSParameterStore struct {
	client parameterStoreClient
	prefix string
}

// NewAWSParameterStore creates a new AWS Systems Manager Parameter Store.
func NewAWSParameterStore(client parameterStoreClient, prefix string) *AWSParameterStore {
	return &AWSParameterStore{client: client, prefix: prefix}
}

// Get retrieves and decrypts a SecureString parameter.
func (a *AWSParameterStore) Get(ctx context.Context, name string) ([]byte, error) {
	resp, err := a.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(a.prefix + name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		var nf *types.ParameterNotFound
		if errors.As(err, &nf) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, fmt.Errorf("failed to get parameter %s: %w", name, err)
	}
	if resp.Parameter == nil || resp.Parameter.Value == nil {
		return nil, fmt.Errorf("parameter %s contains no data", name)
	}
	if resp.Parameter.Type != types.ParameterTypeSecureString {
		return nil, fmt.Errorf("parameter %s is %s, expected SecureString", name, resp.Parameter.Type)
	}
	return []byte(*resp.Parameter.Value), nil
}

// Set stores a secret as a standard-tier SecureString parameter.
func (a *AWSParameterStore) Set(ctx context.Context, name string, data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("parameter %s contains invalid UTF-8", name)
	}
	if len(data) > standardParameterValueLimit {
		return fmt.Errorf("parameter %s is %d bytes, exceeding the standard-tier limit of %d bytes", name, len(data), standardParameterValueLimit)
	}

	_, err := a.client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(a.prefix + name),
		Value:     aws.String(string(data)),
		Type:      types.ParameterTypeSecureString,
		Tier:      types.ParameterTierStandard,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("failed to store parameter %s: %w", name, err)
	}
	return nil
}

// Delete removes a parameter. Deleting a missing parameter is successful.
func (a *AWSParameterStore) Delete(ctx context.Context, name string) error {
	_, err := a.client.DeleteParameter(ctx, &ssm.DeleteParameterInput{Name: aws.String(a.prefix + name)})
	if err != nil {
		var nf *types.ParameterNotFound
		if errors.As(err, &nf) {
			return nil
		}
		return fmt.Errorf("failed to delete parameter %s: %w", name, err)
	}
	return nil
}

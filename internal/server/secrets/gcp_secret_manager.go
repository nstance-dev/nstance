// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"fmt"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GCPSecretManager implements secrets Store using GCP Secret Manager
type GCPSecretManager struct {
	client  *secretmanager.Client
	project string
	prefix  string
}

// NewGCPSecretManagerStore creates a new GCP Secret Manager store
func NewGCPSecretManagerStore(client *secretmanager.Client, project, prefix string) *GCPSecretManager {
	return &GCPSecretManager{
		client:  client,
		project: project,
		prefix:  prefix,
	}
}

// Get retrieves a secret from GCP Secret Manager
func (g *GCPSecretManager) Get(ctx context.Context, name string) ([]byte, error) {
	secretName := g.secretVersionName(name)

	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretName,
	}

	resp, err := g.client.AccessSecretVersion(ctx, req)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, fmt.Errorf("failed to get secret %s: %w", name, err)
	}

	if resp.Payload == nil || len(resp.Payload.Data) == 0 {
		return nil, fmt.Errorf("secret %s contains no data", name)
	}

	return resp.Payload.Data, nil
}

// Set stores a secret in GCP Secret Manager
func (g *GCPSecretManager) Set(ctx context.Context, name string, data []byte) error {
	secretID := g.prefix + name
	parent := fmt.Sprintf("projects/%s", g.project)
	secretPath := fmt.Sprintf("%s/secrets/%s", parent, secretID)

	// Check if secret exists
	_, err := g.client.GetSecret(ctx, &secretmanagerpb.GetSecretRequest{
		Name: secretPath,
	})

	if err != nil {
		if status.Code(err) != codes.NotFound {
			return fmt.Errorf("failed to check secret %s: %w", name, err)
		}

		// Secret doesn't exist, create it
		createReq := &secretmanagerpb.CreateSecretRequest{
			Parent:   parent,
			SecretId: secretID,
			Secret: &secretmanagerpb.Secret{
				Replication: &secretmanagerpb.Replication{
					Replication: &secretmanagerpb.Replication_Automatic_{
						Automatic: &secretmanagerpb.Replication_Automatic{},
					},
				},
			},
		}

		_, createErr := g.client.CreateSecret(ctx, createReq)
		if createErr != nil {
			return fmt.Errorf("failed to create secret %s: %w", name, createErr)
		}
	}

	// Add a new version with the data
	addReq := &secretmanagerpb.AddSecretVersionRequest{
		Parent: secretPath,
		Payload: &secretmanagerpb.SecretPayload{
			Data: data,
		},
	}

	_, err = g.client.AddSecretVersion(ctx, addReq)
	if err != nil {
		return fmt.Errorf("failed to add secret version for %s: %w", name, err)
	}

	return nil
}

// Delete removes a secret from GCP Secret Manager
func (g *GCPSecretManager) Delete(ctx context.Context, name string) error {
	secretID := g.prefix + name
	secretPath := fmt.Sprintf("projects/%s/secrets/%s", g.project, secretID)

	req := &secretmanagerpb.DeleteSecretRequest{
		Name: secretPath,
	}

	err := g.client.DeleteSecret(ctx, req)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil
		}
		return fmt.Errorf("failed to delete secret %s: %w", name, err)
	}

	return nil
}

// List returns all secret names with the given prefix
func (g *GCPSecretManager) List(ctx context.Context) ([]string, error) {
	parent := fmt.Sprintf("projects/%s", g.project)

	req := &secretmanagerpb.ListSecretsRequest{
		Parent: parent,
		Filter: fmt.Sprintf("name:%s", g.prefix),
	}

	var names []string
	it := g.client.ListSecrets(ctx, req)
	for {
		secret, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list secrets: %w", err)
		}
		names = append(names, secret.Name)
	}

	return names, nil
}

// secretVersionName returns the full resource name for the latest version of a secret
func (g *GCPSecretManager) secretVersionName(name string) string {
	secretID := g.prefix + name
	return fmt.Sprintf("projects/%s/secrets/%s/versions/latest", g.project, secretID)
}

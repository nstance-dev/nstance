// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/keys"
	"github.com/nstance-dev/nstance/internal/server/secrets"
)

// NonceRequest contains parameters for generating a registration nonce.
type NonceRequest struct {
	ClusterID string
	Tenant    string
	Expiry    time.Duration
}

// NonceResponse contains the generated nonce JWT.
type NonceResponse struct {
	JWT string
}

// NonceService generates operator registration nonces.
type NonceService struct {
	secretsStore secrets.Store
}

// NewNonceService creates a new NonceService.
func NewNonceService(store secrets.Store) *NonceService {
	return &NonceService{
		secretsStore: store,
	}
}

// Generate creates a new operator registration nonce JWT.
func (s *NonceService) Generate(ctx context.Context, req NonceRequest) (*NonceResponse, error) {
	if req.ClusterID == "" {
		return nil, fmt.Errorf("cluster ID must be specified")
	}
	if req.Tenant == "" {
		return nil, fmt.Errorf("tenant must be specified")
	}

	nonceKeyData, err := s.secretsStore.Get(ctx, "registration-nonce.key")
	if err != nil {
		return nil, fmt.Errorf("load registration nonce key: %w", err)
	}

	noncePrivateKey, err := keys.ParseEd25519PrivateKey(nonceKeyData)
	if err != nil {
		return nil, fmt.Errorf("parse registration nonce key: %w", err)
	}

	jwt, err := instances.NewJWTSigner(noncePrivateKey).GenerateRegistrationNonce(instances.RegistrationNonceParams{
		SubjectID: req.ClusterID,
		Kind:      "operator",
		ClusterID: req.ClusterID,
		Tenant:    req.Tenant,
		Expiry:    req.Expiry,
	})
	if err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	return &NonceResponse{JWT: jwt}, nil
}

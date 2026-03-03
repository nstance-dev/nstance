// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package instances

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTSigner handles signing of registration nonce JWTs
type JWTSigner struct {
	privateKey ed25519.PrivateKey
}

// RegistrationNonceClaims represents the claims in a registration nonce JWT
type RegistrationNonceClaims struct {
	jwt.RegisteredClaims
	Kind       string `json:"kind"`        // "agent" or "operator"
	Sub        string `json:"sub"`         // instance ID or cluster ID
	ConfigHash string `json:"config_hash"` // group runtime config hash at provision time
	ClusterID  string `json:"cluster_id"`  // cluster ID
	Shard      string `json:"shard"`       // shard/zone
	Group      string `json:"group"`       // group key
	OnDemand   bool   `json:"on_demand"`   // is on-demand instance
	Tenant     string `json:"tenant"`      // tenant identifier
}

// NewJWTSigner creates a new JWT signer
func NewJWTSigner(privateKey ed25519.PrivateKey) *JWTSigner {
	return &JWTSigner{
		privateKey: privateKey,
	}
}

// RegistrationNonceParams contains parameters for generating a registration nonce JWT
type RegistrationNonceParams struct {
	SubjectID  string        // Instance ID or cluster ID
	Kind       string        // "agent" or "operator"
	ConfigHash string        // Group runtime config hash at provision time
	ClusterID  string        // Cluster ID
	Shard      string        // Shard/zone
	Group      string        // Group key (empty for operator)
	OnDemand   bool          // Is on-demand instance
	Expiry     time.Duration // JWT expiry duration
	Tenant     string        // Tenant identifier (required)
}

// GenerateRegistrationNonce generates a registration nonce JWT for the given instance/cluster
func (s *JWTSigner) GenerateRegistrationNonce(params RegistrationNonceParams) (string, error) {
	if params.Tenant == "" {
		return "", fmt.Errorf("tenant is required")
	}

	now := time.Now().UTC()
	claims := &RegistrationNonceClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   params.SubjectID,
			ExpiresAt: jwt.NewNumericDate(now.Add(params.Expiry)),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "nstance-server",
		},
		Kind:       params.Kind,
		Sub:        params.SubjectID,
		ConfigHash: params.ConfigHash,
		ClusterID:  params.ClusterID,
		Shard:      params.Shard,
		Group:      params.Group,
		OnDemand:   params.OnDemand,
		Tenant:     params.Tenant,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tokenString, err := token.SignedString(s.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	return tokenString, nil
}

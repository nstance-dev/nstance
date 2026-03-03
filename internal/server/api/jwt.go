// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// RegistrationNonceClaims represents the claims in a registration nonce JWT
type RegistrationNonceClaims struct {
	jwt.RegisteredClaims
	Kind       string `json:"kind"`        // "agent" or "operator"
	Sub        string `json:"sub"`         // instance ID for agents, or cluster ID for operators
	ConfigHash string `json:"config_hash"` // group runtime config hash at provision time
	ClusterID  string `json:"cluster_id"`  // cluster ID
	Shard      string `json:"shard"`       // shard/zone
	Group      string `json:"group"`       // group key
	OnDemand   bool   `json:"on_demand"`   // is on-demand instance
	Tenant     string `json:"tenant"`      // tenant identifier
}

// JWTValidator handles validation of registration nonce JWTs
type JWTValidator struct {
	publicKey ed25519.PublicKey
}

// NewJWTValidator creates a new JWT validator with the given public key
func NewJWTValidator(publicKey ed25519.PublicKey) *JWTValidator {
	return &JWTValidator{
		publicKey: publicKey,
	}
}

// ValidateRegistrationNonce validates a registration nonce JWT and extracts claims
func (v *JWTValidator) ValidateRegistrationNonce(tokenString string, expectedKind string) (*RegistrationNonceClaims, error) {
	// Parse and validate the token
	token, err := jwt.ParseWithClaims(tokenString, &RegistrationNonceClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v (expected EdDSA)", token.Header["alg"])
		}
		return v.publicKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse JWT: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid JWT token")
	}

	claims, ok := token.Claims.(*RegistrationNonceClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}

	// Validate required fields
	if claims.Kind == "" {
		return nil, fmt.Errorf("missing kind field in JWT")
	}
	if claims.Sub == "" {
		return nil, fmt.Errorf("missing sub field in JWT")
	}
	if claims.ClusterID == "" {
		return nil, fmt.Errorf("missing cluster_id field in JWT")
	}

	// Validate kind matches expected
	if claims.Kind != expectedKind {
		return nil, fmt.Errorf("invalid kind: expected %s, got %s", expectedKind, claims.Kind)
	}

	// Validate tenant is present
	if claims.Tenant == "" {
		return nil, fmt.Errorf("missing tenant field in JWT")
	}

	// Validate expiration
	if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("JWT token has expired")
	}

	// Validate not before
	if claims.NotBefore != nil && claims.NotBefore.After(time.Now()) {
		return nil, fmt.Errorf("JWT token not yet valid")
	}

	return claims, nil
}

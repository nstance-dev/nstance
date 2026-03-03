// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"crypto/ed25519"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// GenerateTestJWT generates a JWT for testing purposes with default cluster/shard/tenant
func GenerateTestJWT(privateKey ed25519.PrivateKey, kind, subject string, expiry time.Duration) (string, error) {
	return GenerateTestJWTWithClaims(privateKey, kind, subject, "test-cluster", "test-shard", "", "default", false, expiry)
}

// GenerateTestJWTWithClaims generates a JWT for testing purposes with all claims
func GenerateTestJWTWithClaims(privateKey ed25519.PrivateKey, kind, subject, clusterID, shard, group, tenant string, onDemand bool, expiry time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := &RegistrationNonceClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "nstance-server-test",
		},
		Kind:      kind,
		Sub:       subject,
		ClusterID: clusterID,
		Shard:     shard,
		Group:     group,
		Tenant:    tenant,
		OnDemand:  onDemand,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return token.SignedString(privateKey)
}

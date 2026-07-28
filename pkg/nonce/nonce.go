// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

// Package nonce signs and validates single-use registration credentials.
package nonce

import (
	"crypto/ed25519"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Claims contains the claims carried by a registration nonce.
type Claims struct {
	jwt.RegisteredClaims
	Kind       string `json:"kind"`
	ConfigHash string `json:"config_hash"`
	ClusterID  string `json:"cluster_id"`
	Shard      string `json:"shard"`
	Group      string `json:"group"`
	OnDemand   bool   `json:"on_demand"`
	Tenant     string `json:"tenant"`
}

// Sign signs claims using EdDSA.
func Sign(key ed25519.PrivateKey, claims Claims) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodEdDSA, &claims).SignedString(key)
}

// Validate verifies the EdDSA signature, standard time claims, required
// registration claims, and expected registration kind.
func Validate(token string, key ed25519.PublicKey, kind string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return key, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse nonce: %w", err)
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("invalid nonce")
	}
	if claims.Kind != kind {
		return nil, fmt.Errorf("invalid kind: expected %s, got %s", kind, claims.Kind)
	}
	if claims.Subject == "" || claims.ClusterID == "" || claims.Tenant == "" {
		return nil, fmt.Errorf("nonce requires sub, cluster_id, and tenant")
	}
	return claims, nil
}

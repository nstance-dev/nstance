// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package nonce

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/nstance-dev/nstance/v2/internal/server/keys"
)

// GenerateOperatorToken returns a signed operator registration JWT with a random
// 256-bit nonce ID, valid from now for the positive ttl supplied by the caller.
// It does not register an operator or persist the nonce.
// value must contain a PEM-encoded PKCS#8 Ed25519 private key; its bytes are not
// modified. The returned token is a credential and must not be logged or stored
// in provisioning state.
func GenerateOperatorToken(value []byte, clusterID, tenant string, ttl time.Duration) (string, error) {
	if clusterID == "" || tenant == "" {
		return "", fmt.Errorf("operator nonce requires cluster ID and tenant")
	}
	if ttl <= 0 {
		return "", fmt.Errorf("operator nonce requires a positive TTL")
	}
	key, err := keys.ParseEd25519PrivateKey(value)
	if err != nil {
		return "", err
	}
	defer clear(key)
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate registration nonce ID: %w", err)
	}
	now := time.Now()
	return Sign(key, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "nstance-server", Subject: clusterID, ID: hex.EncodeToString(id[:]),
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Kind: "operator", ClusterID: clusterID, Tenant: tenant,
	})
}

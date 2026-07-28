// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package nonce

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestSignAndValidate verifies registration nonce signing and validation.
func TestSignAndValidate(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	token, err := Sign(privateKey, Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "cluster-1", Issuer: "nstance-server", IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-time.Second)), ExpiresAt: jwt.NewNumericDate(now.Add(30 * time.Minute))},
		Kind:             "operator", ClusterID: "cluster-1", Tenant: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Validate(token, publicKey, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "cluster-1" || claims.ClusterID != "cluster-1" || claims.Tenant != "default" || claims.Kind != "operator" {
		t.Fatalf("claims = %#v", claims)
	}
	wrongKey, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Validate(token, wrongKey, "operator"); err == nil {
		t.Fatal("wrong signature key accepted")
	}
}

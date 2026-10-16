// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package nonce_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/nstance-dev/nstance/v2/pkg/nonce"
)

// TestOperatorNonce checks registration scope, caller-selected expiry, unique
// nonce IDs, signature validation, and preservation of the caller's key bytes.
func TestOperatorNonce(t *testing.T) {
	value, err := nonce.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(value)
	block, _ := pem.Decode(value)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	public := parsed.(ed25519.PrivateKey).Public().(ed25519.PublicKey)
	ids := map[string]bool{}
	for range 2 {
		start := time.Now().Truncate(time.Second)
		token, err := nonce.GenerateOperatorToken(value, "cluster-east", "tenant-west", 7*time.Minute)
		end := time.Now()
		if err != nil {
			t.Fatal(err)
		}
		claims, err := nonce.Validate(token, public, "operator")
		if err != nil {
			t.Fatal(err)
		}
		if claims.Subject != "cluster-east" || claims.ClusterID != "cluster-east" || claims.Tenant != "tenant-west" || claims.Issuer != "nstance-server" || claims.Shard != "" || claims.Group != "" || claims.ConfigHash != "" || claims.OnDemand {
			t.Fatalf("incorrect operator scope: %+v", claims)
		}
		if claims.IssuedAt == nil || claims.NotBefore == nil || claims.ExpiresAt == nil || claims.IssuedAt.Before(start) || claims.IssuedAt.After(end) || !claims.NotBefore.Equal(claims.IssuedAt.Time) || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != 7*time.Minute {
			t.Fatalf("incorrect nonce validity: %+v", claims)
		}
		id, err := hex.DecodeString(claims.ID)
		if err != nil || len(id) != 32 || ids[claims.ID] {
			t.Fatal("nonce ID is not a unique 256-bit value")
		}
		ids[claims.ID] = true
		if _, err := nonce.Validate(token, public, "agent"); err == nil {
			t.Fatal("operator nonce accepted as an agent nonce")
		}
		for _, tc := range []struct {
			at    time.Time
			valid bool
		}{
			{claims.NotBefore.Add(-time.Second), false},
			{claims.NotBefore.Time, true},
			{claims.ExpiresAt.Add(-time.Second), true},
			{claims.ExpiresAt.Time, false},
		} {
			_, err := jwt.ParseWithClaims(token, &nonce.Claims{}, func(*jwt.Token) (any, error) { return public, nil }, jwt.WithTimeFunc(func() time.Time { return tc.at }))
			if (err == nil) != tc.valid {
				t.Fatalf("token validity at %v = %v, want valid=%v", tc.at, err, tc.valid)
			}
		}
	}
	if !bytes.Equal(value, before) {
		t.Fatal("nonce generation modified the caller's key")
	}
	for _, invalid := range []struct {
		clusterID, tenant string
		ttl               time.Duration
	}{
		{"", "tenant-west", time.Minute},
		{"cluster-east", "", time.Minute},
		{"cluster-east", "tenant-west", 0},
		{"cluster-east", "tenant-west", -time.Minute},
	} {
		if _, err := nonce.GenerateOperatorToken(value, invalid.clusterID, invalid.tenant, invalid.ttl); err == nil {
			t.Fatalf("accepted invalid options: %+v", invalid)
		}
	}
}

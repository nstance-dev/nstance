// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package keys

import (
	"testing"
)

func TestEd25519KeyOperations(t *testing.T) {
	// Test Ed25519 key generation
	key, err := GenerateEd25519Key()
	if err != nil {
		t.Fatalf("Failed to generate Ed25519 key: %v", err)
	}

	// Test marshaling
	pemData, err := MarshalEd25519PrivateKey(key)
	if err != nil {
		t.Fatalf("Failed to marshal Ed25519 key: %v", err)
	}

	// Test parsing
	parsedKey, err := ParseEd25519PrivateKey(pemData)
	if err != nil {
		t.Fatalf("Failed to parse Ed25519 key: %v", err)
	}

	// Verify keys are equivalent by comparing bytes
	if string(key) != string(parsedKey) {
		t.Error("Parsed key doesn't match original")
	}
}

func TestEd25519KeySize(t *testing.T) {
	// Ed25519 has fixed key size
	key, err := GenerateEd25519Key()
	if err != nil {
		t.Fatalf("Failed to generate Ed25519 key: %v", err)
	}

	// Ed25519 private keys are always 64 bytes
	if len(key) != 64 {
		t.Errorf("Expected Ed25519 key size 64 bytes, got %d", len(key))
	}
}

func TestParseInvalidKey(t *testing.T) {
	// Test parsing invalid PEM data
	_, err := ParseEd25519PrivateKey([]byte("not a valid key"))
	if err == nil {
		t.Error("Expected error when parsing invalid key")
	}

	// Test parsing wrong key type
	wrongPEM := `-----BEGIN CERTIFICATE-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA7
-----END CERTIFICATE-----`

	_, err = ParseEd25519PrivateKey([]byte(wrongPEM))
	if err == nil {
		t.Error("Expected error when parsing certificate as key")
	}
}

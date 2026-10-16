// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package nonce

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"

	"github.com/nstance-dev/nstance/v2/internal/server/keys"
)

// GenerateSigningKey returns an unencrypted PKCS#8 Ed25519 private key in PEM
// format, as consumed by Nstance's registration signer. The caller must store it
// securely and clear the returned bytes when no longer needed.
func GenerateSigningKey() ([]byte, error) {
	key, err := keys.GenerateEd25519Key()
	if err != nil {
		return nil, err
	}
	defer clear(key)
	return keys.MarshalEd25519PrivateKey(key)
}

// FingerprintSigningKey validates a PKCS#8 Ed25519 private signing key in PEM
// format and returns the lowercase hexadecimal SHA-256 digest of its
// corresponding PKIX public key, not the private key.
// Trailing data, including whitespace after the PEM block, is rejected.
// The input is borrowed and is not modified.
func FingerprintSigningKey(value []byte) (string, error) {
	key, err := keys.ParseEd25519PrivateKey(value)
	if err != nil {
		return "", err
	}
	defer clear(key)
	publicDER, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}
	sum := sha256.Sum256(publicDER)
	return hex.EncodeToString(sum[:]), nil
}

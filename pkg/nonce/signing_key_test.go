// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package nonce_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/v2/pkg/nonce"
)

// TestSigningKeyContract checks PKCS#8 interoperability, public fingerprints,
// and identical key-format rejection by fingerprinting and nonce creation.
func TestSigningKeyContract(t *testing.T) {
	value, err := nonce.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(value)
	block, rest := pem.Decode(value)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		t.Fatal("expected one PKCS#8 private key PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		t.Fatalf("key type = %T, want Ed25519", parsed)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(publicDER))
	fingerprint, err := nonce.FingerprintSigningKey(value)
	if err != nil || fingerprint != want {
		t.Fatalf("fingerprint = %q, %v; want %q", fingerprint, err, want)
	}
	if !bytes.Equal(value, before) {
		t.Fatal("fingerprinting modified the caller's key")
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherDER, err := x509.MarshalPKCS8PrivateKey(other)
	if err != nil {
		t.Fatal(err)
	}
	for name, invalid := range map[string][]byte{
		"not-pem":             []byte("not a private key"),
		"trailing-whitespace": append(bytes.Clone(value), '\n'),
		"multiple-blocks":     append(bytes.Clone(value), value...),
		"wrong-block-type":    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes}),
		"invalid-pkcs8":       pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("invalid")}),
		"non-ed25519":         pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherDER}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := nonce.FingerprintSigningKey(invalid); err == nil {
				t.Fatal("fingerprinted an invalid signing key")
			}
			if _, err := nonce.GenerateOperatorToken(invalid, "cluster-east", "tenant-west", time.Minute); err == nil {
				t.Fatal("signed with an invalid signing key")
			}
		})
	}
}

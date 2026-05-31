// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/nstance-dev/nstance/internal/files"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
}

func TestLoadOrCreateGeneratesKeypair(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create required CA certificate
	caCert := createTestCertificate(t)
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), caCertPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(ca.crt): %v", err)
	}

	nonce := createTestNonce(t)
	if err := os.WriteFile(filepath.Join(dir, nonceFilename), nonce, 0o600); err != nil {
		t.Fatalf("WriteFile(nonce): %v", err)
	}

	identity, err := LoadOrCreate(dir, newTestLogger(), 0o600)
	if err != nil {
		t.Fatalf("LoadOrCreate error = %v", err)
	}
	if identity.PrivateKey == nil || identity.PublicKey == nil {
		t.Fatalf("expected keypair to be generated")
	}

	keyInfo, err := os.Stat(filepath.Join(dir, privateKeyFilename))
	if err != nil {
		t.Fatalf("Stat private key: %v", err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key perms = %o, want 0600", keyInfo.Mode().Perm())
	}

	pubInfo, err := os.Stat(filepath.Join(dir, publicKeyFilename))
	if err != nil {
		t.Fatalf("Stat public key: %v", err)
	}
	if pubInfo.Mode().Perm() != 0o600 {
		t.Fatalf("public key perms = %o, want 0600", pubInfo.Mode().Perm())
	}

	if identity.ClientCert != nil {
		t.Fatalf("IdentityCert = %v, want nil", identity.ClientCert)
	}
}

func TestLoadOrCreateReadsExistingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := newTestLogger()

	// Create required CA certificate
	caCert := createTestCertificate(t)
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), caCertPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(ca.crt): %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	pubPEM, privPEM, _, err := files.EncodePem(pub, priv, nil)
	if err != nil {
		t.Fatalf("EncodePem: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, publicKeyFilename), pubPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(pub): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, privateKeyFilename), privPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(priv): %v", err)
	}

	identity, err := LoadOrCreate(dir, logger, 0o600)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if identity.PrivateKey == nil || identity.PublicKey == nil {
		t.Fatalf("expected existing keypair to be loaded")
	}
	if !bytes.Equal([]byte(*identity.PublicKey), []byte(pub)) {
		t.Fatalf("loaded public key differs from stored key")
	}
	if !bytes.Equal([]byte(*identity.PrivateKey), []byte(priv)) {
		t.Fatalf("loaded private key differs from stored key")
	}
}

func TestIdentityStoreIdentityCertificate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create required CA certificate
	caCert := createTestCertificate(t)
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), caCertPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(ca.crt): %v", err)
	}

	nonce := createTestNonce(t)
	if err := os.WriteFile(filepath.Join(dir, nonceFilename), nonce, 0o600); err != nil {
		t.Fatalf("WriteFile(nonce): %v", err)
	}

	identity, err := LoadOrCreate(dir, newTestLogger(), 0o600)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	cert := createTestCertificate(t)
	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})

	if err := identity.StoreClientCertificate(clientCertPEM); err != nil {
		t.Fatalf("StoreClientCertificate: %v", err)
	}
	if identity.ClientCert == nil {
		t.Fatalf("ClientCert is nil after storing")
	}

	data, err := os.ReadFile(filepath.Join(dir, clientCertFilename))
	if err != nil {
		t.Fatalf("ReadFile(cert): %v", err)
	}
	if string(data) != string(clientCertPEM) {
		t.Fatalf("client certificate on disk differs from stored value")
	}
}

func TestIdentityNonceLifecycle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create required CA certificate
	caCert := createTestCertificate(t)
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), caCertPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(ca.crt): %v", err)
	}

	nonce := createTestNonce(t)
	if err := os.WriteFile(filepath.Join(dir, nonceFilename), nonce, 0o600); err != nil {
		t.Fatalf("WriteFile(nonce): %v", err)
	}

	identity, err := LoadOrCreate(dir, newTestLogger(), 0o600)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	if err := identity.DeleteNonce(); err != nil {
		t.Fatalf("DeleteNonce: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, nonceFilename)); !os.IsNotExist(err) {
		t.Fatalf("nonce file still exists, err = %v", err)
	}
}

func TestLoadOrCreateErrorsOnIncompleteKeypairWithNonce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := newTestLogger()

	// Create required CA certificate
	caCert := createTestCertificate(t)
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), caCertPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(ca.crt): %v", err)
	}

	nonce := createTestNonce(t)
	if err := os.WriteFile(filepath.Join(dir, nonceFilename), nonce, 0o600); err != nil {
		t.Fatalf("WriteFile(nonce): %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubPEM, _, _, err := files.EncodePem(pub, priv, nil)
	if err != nil {
		t.Fatalf("EncodePem: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, publicKeyFilename), pubPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(pub): %v", err)
	}

	// Incomplete keypair (public key only) should error - we never want to overwrite
	_, err = LoadOrCreate(dir, logger, 0o600)
	if err == nil {
		t.Fatalf("expected LoadOrCreate to error on incomplete keypair")
	}
	if !strings.Contains(err.Error(), "incomplete identity keypair") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadOrCreateErrorsOnIncompleteKeypairAfterRegistration(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := newTestLogger()

	// Create required CA certificate
	caCert := createTestCertificate(t)
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), caCertPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(ca.crt): %v", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	_, privPEM, _, err := files.EncodePem(nil, priv, nil)
	if err != nil {
		t.Fatalf("EncodePem: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, privateKeyFilename), privPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(priv): %v", err)
	}

	cert := createTestCertificate(t)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(filepath.Join(dir, clientCertFilename), certPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(cert): %v", err)
	}

	_, err = LoadOrCreate(dir, logger, 0o600)
	if err == nil {
		t.Fatalf("expected LoadOrCreate to error")
	}
	if !strings.Contains(err.Error(), "incomplete identity keypair") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func createTestCertificate(t *testing.T) *x509.Certificate {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
	}

	raw, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}

	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return cert
}

func createTestNonce(t *testing.T) []byte {
	t.Helper()

	token, err := jwt.NewBuilder().
		Subject("test").
		Expiration(time.Now().Add(time.Minute)).
		Build()
	if err != nil {
		t.Fatalf("Build nonce token: %v", err)
	}

	nonce, err := jwt.Sign(token, jwt.WithKey(jwa.HS256, []byte("secret")))
	if err != nil {
		t.Fatalf("Sign nonce token: %v", err)
	}
	return nonce
}

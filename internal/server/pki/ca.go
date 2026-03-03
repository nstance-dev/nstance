// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package pki

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/keys"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// LoadCA attempts to load existing CA certificate and private key
func LoadCA(ctx context.Context, storageClient storage.Storage, secretsStore secrets.Store, logger *slog.Logger) ([]byte, []byte, bool, error) {
	// Try to load existing CA private key and certificate
	caKeyPEM, keyErr := secretsStore.Get(ctx, "ca.key")
	caCertPEM, _, certErr := storageClient.Get(ctx, "ca.crt")

	// If both exist, we're done
	if keyErr == nil && certErr == nil {
		logger.Debug("CA certificate and private key loaded successfully")
		return caCertPEM, caKeyPEM, false, nil
	}

	// If only one exists, that's an inconsistent state, so return an error
	if (keyErr == nil && certErr != nil) || (keyErr != nil && certErr == nil) {
		return nil, nil, false, fmt.Errorf("inconsistent CA state: cert exists=%t, key exists=%t", certErr == nil, keyErr == nil)
	}

	// Both failed to load - check if it's "not found" vs actual error
	if !errors.Is(keyErr, storage.ErrNotFound) {
		return nil, nil, false, fmt.Errorf("failed to load CA private key: %w", keyErr)
	}
	if !errors.Is(certErr, storage.ErrNotFound) {
		return nil, nil, false, fmt.Errorf("failed to load CA certificate: %w", certErr)
	}

	// Both don't exist - return nil, nil, true, nil to indicate need to generate
	return nil, nil, true, nil
}

// GenerateCA creates a new CA certificate and private key and stores them
func GenerateCA(ctx context.Context, storageClient storage.Storage, secretsStore secrets.Store, caCertConfig *config.CertConfig, vars map[string]string, logger *slog.Logger) ([]byte, []byte, error) {
	logger.Info("Generating new CA certificate and private key")

	// Generate the CA cert and key
	caCertPEM, caKeyPEM, err := generateCACertAndKey(caCertConfig, vars)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA: %w", err)
	}

	// Store both in their respective places
	if err := secretsStore.Set(ctx, "ca.key", caKeyPEM); err != nil {
		return nil, nil, fmt.Errorf("failed to store CA private key: %w", err)
	}

	if err := storageClient.Put(ctx, "ca.crt", caCertPEM); err != nil {
		return nil, nil, fmt.Errorf("failed to store CA certificate: %w", err)
	}

	logger.Info("CA certificate and private key generated and stored successfully")
	return caCertPEM, caKeyPEM, nil
}

// generateCACertAndKey creates a new CA certificate and private key (internal function)
func generateCACertAndKey(caCertConfig *config.CertConfig, vars map[string]string) ([]byte, []byte, error) {
	// Generate Ed25519 CA private key
	caPublicKey, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA private key: %w", err)
	}

	// Create CA certificate template
	// If no config provided, use sensible defaults with vars
	var cn, org string
	var ttl int

	if caCertConfig != nil {
		// Convert config to internal format for processing
		internalConfig := CertificateConfig{
			Kind:         "server", // CA is a special type of server cert
			CN:           caCertConfig.CN,
			Organization: caCertConfig.Organization,
			TTL:          caCertConfig.TTL,
		}

		// Process templates
		data := CertificateTemplateData{
			Vars: vars,
		}

		processed, err := ProcessCertificateTemplate(internalConfig, data)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to process CA certificate template: %w", err)
		}

		if processed.CN != nil {
			cn = *processed.CN
		}
		if len(processed.Organization) > 0 {
			org = processed.Organization[0]
		}
		ttl = processed.TTL
	}

	// Fallback defaults if still empty
	if cn == "" {
		cn = "Nstance CA"
	}
	if org == "" {
		org = "Nstance Cluster Unknown"
	}
	if ttl == 0 {
		ttl = 87600 // 10 years default
	}

	// Create CA certificate template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{org},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
			CommonName:    cn,
		},
		NotBefore:             time.Now().UTC(),
		NotAfter:              time.Now().UTC().Add(time.Duration(ttl) * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
		MaxPathLenZero:        false,
	}

	// Generate CA certificate (self-signed)
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, caPublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA certificate: %w", err)
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key to PEM using secrets utility
	keyPEM, err := keys.MarshalEd25519PrivateKey(caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode CA private key: %w", err)
	}

	return certPEM, keyPEM, nil
}

// EnsureRegistrationNonceKey loads or generates registration nonce private key (if server is cluster leader)
func EnsureRegistrationNonceKey(ctx context.Context, secretsStore secrets.Store, isClusterLeader bool, logger *slog.Logger) ([]byte, error) {
	// Try to load existing registration nonce private key
	nonceKeyPEM, err := secretsStore.Get(ctx, "registration-nonce.key")

	// If found, return
	if err == nil {
		logger.Info("Registration nonce private key loaded successfully")
		return nonceKeyPEM, nil
	}

	// If any error occured other than not found, return it to trigger a restart/retry on loading the key
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("failed to load registration nonce key: %w", err)
	}

	// Key doesn't exist, but we must ensure only the cluster leader will create it
	if !isClusterLeader {
		return nil, fmt.Errorf("registration nonce key missing and this server is not the leader")
	}

	// We are the cluster leader, so generate a new registration nonce key
	logger.Info("Generating new registration nonce private key as leader")
	privateKey, err := keys.GenerateEd25519Key()
	if err != nil {
		return nil, fmt.Errorf("failed to generate registration nonce private key: %w", err)
	}
	nonceKeyPEM, err = keys.MarshalEd25519PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encode registration nonce private key: %w", err)
	}
	if err := secretsStore.Set(ctx, "registration-nonce.key", nonceKeyPEM); err != nil {
		return nil, fmt.Errorf("failed to store registration nonce private key: %w", err)
	}
	logger.Info("Registration nonce private key generated and stored successfully")
	return nonceKeyPEM, nil
}

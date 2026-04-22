// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package pki

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"time"

	"github.com/nstance-dev/nstance/internal/server/keys"
)

// GenerateClientCertificate creates a client certificate signed by the CA with a specified TTL.
// The role parameter is encoded as a custom extension (OID: 1.3.6.1.4.1.999999.1) for authorization.
// The tenant parameter is stored in the Organization (O) field for multi-tenancy.
func GenerateClientCertificate(caCertPEM, caKeyPEM, clientPublicKeyPEM []byte, clientID, role, tenant string, ttlHours int) ([]byte, time.Time, error) {
	// Parse CA certificate
	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return nil, time.Time{}, fmt.Errorf("failed to decode CA certificate PEM")
	}

	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// Parse CA private key using keys utility
	caKey, err := keys.ParseEd25519PrivateKey(caKeyPEM)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to parse CA private key: %w", err)
	}

	// Parse client public key
	clientPubKeyBlock, _ := pem.Decode(clientPublicKeyPEM)
	if clientPubKeyBlock == nil {
		return nil, time.Time{}, fmt.Errorf("failed to decode client public key PEM")
	}

	clientPubKey, err := x509.ParsePKIXPublicKey(clientPubKeyBlock.Bytes)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to parse client public key: %w", err)
	}

	ed25519Key, ok := clientPubKey.(ed25519.PublicKey)
	if !ok {
		return nil, time.Time{}, fmt.Errorf("client public key must be Ed25519")
	}

	if tenant == "" {
		return nil, time.Time{}, fmt.Errorf("tenant is required")
	}

	// Create client certificate template
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(ttlHours) * time.Hour)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UTC().UnixNano()),
		Subject: pkix.Name{
			CommonName:   clientID,
			Organization: []string{tenant}, // Tenant stored in Organization field
		},
		NotBefore:             now,
		NotAfter:              expiresAt,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	// Add role as a custom extension for authorization
	roleOID := []int{1, 3, 6, 1, 4, 1, 999999, 1}
	roleBytes, err := asn1.Marshal(role)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to marshal role extension: %w", err)
	}

	template.ExtraExtensions = []pkix.Extension{
		{
			Id:       roleOID,
			Critical: false,
			Value:    roleBytes,
		},
	}

	// Generate certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, ed25519Key, caKey)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to generate client certificate: %w", err)
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	return certPEM, expiresAt, nil
}

// ExtractCertSerial parses a PEM-encoded certificate and returns its serial number as a string.
func ExtractCertSerial(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert.SerialNumber.String(), nil
}

// GenerateClientCertificateWithConfig creates a client certificate with detailed configuration
// This is used for batch certificate generation where advanced customization is needed
func GenerateClientCertificateWithConfig(caCertPEM, caKeyPEM, clientPublicKeyPEM []byte, clientID string, config *CertificateConfig) ([]byte, time.Time, error) {
	// Parse CA certificate
	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return nil, time.Time{}, fmt.Errorf("failed to decode CA certificate PEM")
	}

	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// Parse CA private key using keys utility
	caKey, err := keys.ParseEd25519PrivateKey(caKeyPEM)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to parse CA private key: %w", err)
	}

	// Parse client public key
	clientPubKeyBlock, _ := pem.Decode(clientPublicKeyPEM)
	if clientPubKeyBlock == nil {
		return nil, time.Time{}, fmt.Errorf("failed to decode client public key PEM")
	}

	clientPubKey, err := x509.ParsePKIXPublicKey(clientPubKeyBlock.Bytes)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to parse client public key: %w", err)
	}

	ed25519Key, ok := clientPubKey.(ed25519.PublicKey)
	if !ok {
		return nil, time.Time{}, fmt.Errorf("client public key must be Ed25519")
	}

	if config == nil {
		return nil, time.Time{}, fmt.Errorf("certificate config is required")
	}

	// Extract config values
	commonName := *config.CN
	if commonName == "" {
		return nil, time.Time{}, fmt.Errorf("common name is required in certificate config")
	}

	validityPeriod := time.Duration(config.TTL) * time.Hour
	if validityPeriod <= 0 {
		validityPeriod = 8760 * time.Hour // Default to 1 year if not specified
	}

	// Create certificate template
	now := time.Now().UTC()
	expiresAt := now.Add(validityPeriod)

	// Determine extended key usage based on certificate kind
	var extKeyUsage []x509.ExtKeyUsage
	switch config.Kind {
	case "server":
		extKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	case "client":
		extKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	default:
		// Default to client auth if kind is not specified
		extKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UTC().UnixNano()),
		Subject: pkix.Name{
			CommonName:    commonName,
			Organization:  config.Organization,
			Country:       config.Country,
			Province:      config.Province,
			Locality:      config.Locality,
			StreetAddress: config.Street,
			PostalCode:    config.PostalCode,
		},
		DNSNames:              config.DNS,
		NotBefore:             now,
		NotAfter:              expiresAt,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           extKeyUsage,
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	// Parse and add IP addresses
	for _, ipStr := range config.IP {
		if ip := net.ParseIP(ipStr); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		}
	}

	// Parse and add URI SANs
	for _, uriStr := range config.URI {
		u, err := url.Parse(uriStr)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("failed to parse URI SAN %q: %w", uriStr, err)
		}
		if u.Scheme == "" {
			return nil, time.Time{}, fmt.Errorf("URI SAN %q must be an absolute URI with a scheme", uriStr)
		}
		template.URIs = append(template.URIs, u)
	}

	// Generate certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, ed25519Key, caKey)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to generate client certificate: %w", err)
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	return certPEM, expiresAt, nil
}

// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package pki

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/nstance-dev/nstance/internal/server/keys"
)

// GenerateServerCertificate creates a server certificate signed by the CA.
// extraSANs are optional additional IP addresses or DNS names to include in the certificate.
func GenerateServerCertificate(caCertPEM, caKeyPEM []byte, bindAddress string, extraSANs ...string) ([]byte, []byte, error) {
	// Parse CA certificate
	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode CA certificate PEM")
	}

	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// Parse CA private key using secrets utility
	caKey, err := keys.ParseEd25519PrivateKey(caKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA private key: %w", err)
	}

	// Generate Ed25519 server key pair
	serverPublicKey, serverKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate server key: %w", err)
	}

	// Extract IP address and port from bind address for certificate
	host, _, err := net.SplitHostPort(bindAddress)
	if err != nil {
		host = bindAddress // fallback if no port specified
	}

	// Create server certificate template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UTC().UnixNano()),
		Subject: pkix.Name{
			Organization:  []string{"Nstance Server"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
			CommonName:    "nstance-server",
		},
		NotBefore:             time.Now().UTC(),
		NotAfter:              time.Now().UTC().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	// Add host as IP address or DNS name to SAN
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	// Also always include common addresses
	template.DNSNames = append(template.DNSNames, "nstance-server", "localhost")
	template.IPAddresses = append(template.IPAddresses, net.IPv4(127, 0, 0, 1), net.IPv6loopback)

	// Add extra SANs (e.g. advertise address)
	for _, san := range extraSANs {
		h, _, err := net.SplitHostPort(san)
		if err != nil {
			h = san
		}
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else if h != "" {
			template.DNSNames = append(template.DNSNames, h)
		}
	}

	// Generate certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, serverPublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate server certificate: %w", err)
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key to PEM using secrets utility
	keyPEM, err := keys.MarshalEd25519PrivateKey(serverKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode private key: %w", err)
	}

	return certPEM, keyPEM, nil
}

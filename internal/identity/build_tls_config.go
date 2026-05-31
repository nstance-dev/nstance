// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/nstance-dev/nstance/internal/files"
)

// BuildTLSConfig creates a TLS config using the identity's certificate and key.
// Requires CACert, ClientCert, and PrivateKey to be set.
func (i *Identity) BuildTLSConfig() (*tls.Config, error) {
	if i.CACert == nil {
		return nil, fmt.Errorf("CA certificate not loaded")
	}
	if i.ClientCert == nil {
		return nil, fmt.Errorf("client certificate not loaded")
	}
	if i.PrivateKey == nil {
		return nil, fmt.Errorf("private key not loaded")
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(i.CACert)

	_, keyPEM, _, err := files.EncodePem(nil, *i.PrivateKey, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to encode private key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: i.ClientCert.Raw})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to create X509 key pair: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   minTLSVersion,
	}, nil
}

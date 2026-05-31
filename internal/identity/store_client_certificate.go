// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/nstance-dev/nstance/internal/files"
)

// StoreClientCertificate saves the provided certificate and updates the in-memory copy.
func (i *Identity) StoreClientCertificate(certPEM []byte) error {
	if len(certPEM) == 0 {
		return fmt.Errorf("certificate PEM is empty")
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("invalid certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	if err := files.WritePEM(i.dir, clientCertFilename, string(certPEM), i.mode); err != nil {
		i.logger.Error("error writing identity certificate file", "err", err)
		return fmt.Errorf("failed to store identity certificate")
	}

	i.ClientCert = cert
	return nil
}

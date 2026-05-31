// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto/ed25519"
	"crypto/x509"
	"fmt"
	"log/slog"

	"github.com/nstance-dev/nstance/internal/files"
)

// Load loads identity material from dir for use as a client.
// Requires ca.crt, identity.crt, and identity.key to exist.
// Returns an error if any required file is missing.
func Load(dir string, logger *slog.Logger) (*Identity, error) {
	identity := &Identity{dir: dir, logger: logger, mode: 0600}

	// Load CA cert - required
	caCertValue, err := files.LoadPEM(logger, dir, caCertFilename, true)
	if err != nil {
		return nil, fmt.Errorf("identity not found at %s: %w (run 'nstance-admin cluster register-operator' first)", dir, err)
	}
	ca, ok := caCertValue.(*x509.Certificate)
	if !ok {
		return nil, fmt.Errorf("unexpected type in %s/%s", dir, caCertFilename)
	}
	identity.CACert = ca

	// Load client cert - required
	clientCertValue, err := files.LoadPEM(logger, dir, clientCertFilename, true)
	if err != nil {
		return nil, fmt.Errorf("client certificate not found at %s: %w", dir, err)
	}
	cert, ok := clientCertValue.(*x509.Certificate)
	if !ok {
		return nil, fmt.Errorf("unexpected type in %s/%s", dir, clientCertFilename)
	}
	identity.ClientCert = cert

	// Load private key - required
	keyValue, err := files.LoadPEM(logger, dir, privateKeyFilename, true)
	if err != nil {
		return nil, fmt.Errorf("private key not found at %s: %w", dir, err)
	}
	key, ok := keyValue.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("unexpected type in %s/%s", dir, privateKeyFilename)
	}
	identity.PrivateKey = &key

	return identity, nil
}

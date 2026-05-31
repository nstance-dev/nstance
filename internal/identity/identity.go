// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"os"
)

const (
	nonceFilename      = "nonce.jwt"
	caCertFilename     = "ca.crt"
	publicKeyFilename  = "identity.pub"
	privateKeyFilename = "identity.key"
	clientCertFilename = "identity.crt"
	minTLSVersion      = tls.VersionTLS13
)

// Identity represents the identity material stored on disk for the agent.
type Identity struct {
	dir    string
	logger *slog.Logger
	mode   os.FileMode

	Nonce      []byte
	CACert     *x509.Certificate
	PublicKey  *ed25519.PublicKey
	PrivateKey *ed25519.PrivateKey
	ClientCert *x509.Certificate
}

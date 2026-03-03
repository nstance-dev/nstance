// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"

	"github.com/nstance-dev/nstance/internal/files"
)

// GenerateKeypair creates a new identity keypair, stores it on disk, and updates the struct.
func (i *Identity) GenerateKeypair() error {
	if i.PublicKey != nil && i.PrivateKey != nil {
		return fmt.Errorf("identity keypair already exists")
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		i.logger.Error("failed to generate identity keypair", "err", err)
		return fmt.Errorf("failed to generate identity keypair")
	}

	pubPEM, privPEM, _, err := files.EncodePem(pub, priv, nil)
	if err != nil {
		i.logger.Error("failed to encode identity keypair", "err", err)
		return fmt.Errorf("failed to generate identity keypair")
	}

	if err := files.WritePEM(i.dir, publicKeyFilename, string(pubPEM), i.mode); err != nil {
		i.logger.Error("error writing identity public key file", "err", err)
		return fmt.Errorf("failed to generate identity keypair")
	}
	if err := files.WritePEM(i.dir, privateKeyFilename, string(privPEM), i.mode); err != nil {
		i.logger.Error("error writing identity private key file", "err", err)
		return fmt.Errorf("failed to generate identity keypair")
	}

	i.PublicKey = &pub
	i.PrivateKey = &priv
	return nil
}

// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto/ed25519"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/nstance-dev/nstance/internal/files"
)

// LoadOrCreate loads identity material from dir. Missing files are left nil, errors
// reading existing files are returned. If the identity keypair is missing it is
// generated automatically.
func LoadOrCreate(dir string, logger *slog.Logger, mode os.FileMode) (*Identity, error) {
	if mode == 0 {
		return nil, fmt.Errorf("mode cannot be 0")
	}

	identity := &Identity{dir: dir, logger: logger, mode: mode}

	// load nonce - not required
	nonce, err := files.LoadJWT(logger, dir, nonceFilename, false)
	if err != nil {
		return nil, err
	}
	identity.Nonce = nonce
	if len(identity.Nonce) > 0 {
		if err := identity.validateAndExtractNonce(); err != nil {
			return nil, err
		}
	}

	// load CA cert - not required (may not exist yet during bootstrap)
	caCertValue, err := files.LoadPEM(logger, dir, caCertFilename, false)
	if err != nil {
		return nil, err
	}
	if caCertValue != nil {
		ca, ok := caCertValue.(*x509.Certificate)
		if !ok {
			return nil, fmt.Errorf("unexpected certificate type in %s", filepath.Join(dir, caCertFilename))
		}
		identity.CACert = ca
	}

	// load pub key - not required
	pubValue, err := files.LoadPEM(logger, dir, publicKeyFilename, false)
	if err != nil {
		return nil, err
	}
	if pubValue != nil {
		pub, ok := pubValue.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("unexpected public key type in %s", filepath.Join(dir, publicKeyFilename))
		}
		identity.PublicKey = &pub
	}

	// load private key - not required
	keyValue, err := files.LoadPEM(logger, dir, privateKeyFilename, false)
	if err != nil {
		return nil, err
	}
	if keyValue != nil {
		key, ok := keyValue.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("unexpected private key type in %s", filepath.Join(dir, privateKeyFilename))
		}
		identity.PrivateKey = &key
	}

	// load client cert - not required
	clientCertValue, err := files.LoadPEM(logger, dir, clientCertFilename, false)
	if err != nil {
		return nil, err
	}
	if clientCertValue != nil {
		cert, ok := clientCertValue.(*x509.Certificate)
		if !ok {
			return nil, fmt.Errorf("unexpected certificate type in %s", filepath.Join(dir, clientCertFilename))
		}
		identity.ClientCert = cert
	}

	// generate keypair only if both are missing
	if identity.PublicKey == nil && identity.PrivateKey == nil {
		if err := identity.GenerateKeypair(); err != nil {
			return nil, err
		}
	} else if identity.PublicKey == nil || identity.PrivateKey == nil {
		// one key exists but not the other - this is a corrupt state we can't recover from
		return nil, fmt.Errorf("incomplete identity keypair: public key found=%t, private key found=%t",
			identity.PublicKey != nil, identity.PrivateKey != nil)
	}

	return identity, nil
}

// validateAndExtractNonce validates the nonce JWT and extracts the config hash in a single parse.
func (i *Identity) validateAndExtractNonce() error {
	if len(i.Nonce) == 0 {
		return fmt.Errorf("nonce is empty")
	}

	token, err := jwt.Parse(i.Nonce, jwt.WithVerify(false), jwt.WithValidate(true))
	if err != nil {
		return fmt.Errorf("nonce is not a valid JWT: %w", err)
	}

	// Extract and save config hash if present
	if configHash, ok := token.Get("config_hash"); ok {
		if hash, ok := configHash.(string); ok && hash != "" {
			if err := files.WriteString(i.dir, "config.hash", hash, i.mode); err != nil {
				return fmt.Errorf("failed to write config hash: %w", err)
			}
			i.logger.Info("Saved config hash from nonce", "hash", hash)
		}
	}

	return nil
}

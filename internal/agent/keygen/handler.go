// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package keygen

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"os"
	"path"

	"github.com/nstance-dev/nstance/internal/files"
	"github.com/nstance-dev/nstance/pkg/client/agent"
)

// Handler implements the KeyRequestHandler interface for processing key generation requests
type Handler struct {
	logger      *slog.Logger
	keysDir     string
	keyMode     os.FileMode
	agentClient *agent.Client
}

// New creates a new key request handler
func New(logger *slog.Logger, keysDir string, keyMode os.FileMode, agentClient *agent.Client) *Handler {
	return &Handler{
		logger:      logger,
		keysDir:     keysDir,
		keyMode:     keyMode,
		agentClient: agentClient,
	}
}

// HandleKeyRequest generates the requested keys and submits public keys to server
func (h *Handler) HandleKeyRequest(keyNames []string) error {
	if len(keyNames) == 0 {
		return nil
	}

	h.logger.Info("generating requested keys", "keys", keyNames)

	publicKeys := make(map[string]string)
	newCount := 0

	for _, name := range keyNames {
		pubKeyPEM, isNew, err := h.ensureKey(name)
		if err != nil {
			h.logger.Error("failed to generate key", "name", name, "error", err)
			return fmt.Errorf("failed to generate key %q: %w", name, err)
		}

		publicKeys[name] = pubKeyPEM
		if isNew {
			newCount++
		}
	}

	h.logger.Info("key generation completed",
		"total_keys", len(publicKeys),
		"new_keys", newCount)

	// Submit public keys to server if any were generated
	if len(publicKeys) > 0 {
		ctx := context.Background() // Use background context for key submission
		if err := h.agentClient.SubmitPublicKeys(ctx, publicKeys); err != nil {
			h.logger.Error("failed to submit public keys", "error", err)
			return fmt.Errorf("failed to submit public keys: %w", err)
		}
		h.logger.Info("public keys submitted successfully", "count", len(publicKeys))
	}

	return nil
}

// ensureKey checks if a keypair exists, and if not it generates a new ed25519
// keypair and writes the public and private keys to the keys directory.
func (h *Handler) ensureKey(name string) (string, bool, error) {
	isNew := false
	// validate the name
	if path.Base(name) != name {
		return "", isNew, fmt.Errorf("invalid keypair name: %s", name)
	}
	pubName := name + ".pub"
	keyName := name + ".key"

	// check if the keypair already exists
	pubKeyAny, err := files.LoadPEM(h.logger, h.keysDir, pubName, false)
	if err != nil {
		return "", isNew, err
	}
	var pubKey ed25519.PublicKey
	if pubKeyAny != nil {
		var ok bool
		pubKey, ok = pubKeyAny.(ed25519.PublicKey)
		if !ok {
			return "", isNew, fmt.Errorf("unexpected public key type for %s", pubName)
		}
	}
	keyPemAny, err := files.LoadPEM(h.logger, h.keysDir, keyName, false)
	if err != nil {
		return "", isNew, err
	}
	keyExists := keyPemAny != nil

	// if both files exist, return the public key. error if one exists.
	if pubKey != nil && keyExists {
		// encode existing public key to PEM
		pubPEM, _, _, err := files.EncodePem(pubKey, nil, nil)
		if err != nil {
			return "", isNew, fmt.Errorf("failed to encode public key: %w", err)
		}
		return string(pubPEM), isNew, nil
	} else if pubKey == nil && keyExists {
		return "", isNew, fmt.Errorf("inconsistent keypair file existance: '%s' and '%s'", pubName, keyName)
	}

	// generate the keypair
	isNew = true
	pubData, keyData, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", isNew, err
	}
	// encode using PEM
	newPubPem, newKeyPem, _, err := files.EncodePem(pubData, keyData, nil)
	if err != nil {
		return "", isNew, fmt.Errorf("failed to encode keypair: %w", err)
	}
	// write the keypair to disk
	err = files.WritePEM(h.keysDir, pubName, string(newPubPem), h.keyMode)
	if err != nil {
		return "", isNew, err
	}
	err = files.WritePEM(h.keysDir, keyName, string(newKeyPem), h.keyMode)
	if err != nil {
		return "", isNew, err
	}
	// return the public key PEM
	return string(newPubPem), isNew, nil
}

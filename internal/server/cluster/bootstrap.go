// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/election"
	"github.com/nstance-dev/nstance/internal/server/pki"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// ErrCAGenerationRequiresLeadership is returned when CA needs generation but this server is not leader.
var ErrCAGenerationRequiresLeadership = errors.New("CA certificate needs generating, but this instance is not cluster leader")

// BootstrapConfig contains configuration for cluster bootstrap.
type BootstrapConfig struct {
	// ElectionManager manages leader elections
	ElectionManager *election.Manager
	// ElectionConfig is the configuration for starting the cluster election
	ElectionConfig election.ElectionConfig
	// Storage is the cluster-scoped storage
	Storage storage.Storage
	// SecretsStore for encrypted secrets
	SecretsStore secrets.Store
	// CAConfig is the optional CA certificate configuration
	CAConfig *config.CertConfig
	// TemplateVars for CA generation
	TemplateVars map[string]string
	// Logger for bootstrap events
	Logger *slog.Logger
}

// BootstrapResult contains the results of cluster bootstrap.
type BootstrapResult struct {
	CACert []byte
	CAKey  []byte
}

// Bootstrap initializes cluster-level resources including CA and leader election.
// It handles:
// 1. Loading existing CA or determining if generation is needed
// 2. Starting leader election (if enabled)
// 3. Generating CA if this server becomes leader and CA is needed
// 4. Enabling peer mode once CA is available
// 5. Ensuring registration nonce key exists
//
// Returns a BootstrapResult with CA cert and key, or an error.
// The caller is responsible for stopping the election manager.
func Bootstrap(ctx context.Context, cfg BootstrapConfig, leaderElectionEnabled bool) (*BootstrapResult, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	if leaderElectionEnabled && cfg.ElectionManager == nil {
		return nil, fmt.Errorf("election manager is required when leader election is enabled")
	}

	// 1. Try to load existing CA first
	caCertData, caKeyData, needToGenerateCA, err := pki.LoadCA(ctx, cfg.Storage, cfg.SecretsStore, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to load CA certificate: %w", err)
	}

	if !leaderElectionEnabled {
		logger.Info("Cluster leader election disabled")

		// Verify required secrets exist when leader election is disabled
		requiredSecrets := []string{"ca.crt", "ca.key", "registration-nonce.key"}
		for _, secret := range requiredSecrets {
			var exists bool
			if secret == "ca.crt" {
				exists, _ = cfg.Storage.Exists(ctx, secret)
			} else {
				_, err := cfg.SecretsStore.Get(ctx, secret)
				exists = err == nil
			}
			if !exists {
				return nil, fmt.Errorf("required secret missing (cluster leader election disabled requires pre-provisioned secrets): %s", secret)
			}
		}
		logger.Info("All required pre-provisioned secrets verified")
	} else {
		// 2. Start cluster leader election
		electionCfg := cfg.ElectionConfig
		electionCfg.PeerMode = !needToGenerateCA // Enable peer mode only if we have CA
		electionCfg.CACert = caCertData          // nil if needToGenerateCA

		if err := cfg.ElectionManager.StartClusterElection(ctx, electionCfg); err != nil {
			return nil, fmt.Errorf("failed to start cluster leader election: %w", err)
		}

		// 3. Wait for initial election to complete
		if err := cfg.ElectionManager.WaitForClusterElection(ctx); err != nil {
			return nil, fmt.Errorf("failed to complete initial election: %w", err)
		}

		// 4. Handle CA generation if needed
		if needToGenerateCA {
			if !cfg.ElectionManager.IsClusterLeader() {
				return nil, ErrCAGenerationRequiresLeadership
			}

			// We're cluster leader - generate CA
			caCertData, caKeyData, err = pki.GenerateCA(ctx, cfg.Storage, cfg.SecretsStore, cfg.CAConfig, cfg.TemplateVars, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to generate CA certificate: %w", err)
			}

			// Enable peer mode now that we have CA
			if err := cfg.ElectionManager.EnableClusterPeerMode(caCertData); err != nil {
				return nil, fmt.Errorf("failed to enable peer mode: %w", err)
			}
			logger.Info("Generated CA certificate and enabled peer mode")
		}
	}

	logger.Info("CA key and certificate ready")

	// Ensure registration nonce key exists (or create if leader) and warm the cache
	isLeader := cfg.ElectionManager != nil && cfg.ElectionManager.IsClusterLeader()
	if _, err := pki.EnsureRegistrationNonceKey(ctx, cfg.SecretsStore, isLeader, logger); err != nil {
		return nil, fmt.Errorf("failed to ensure registration nonce key: %w", err)
	}
	logger.Info("Registration nonce key ready")

	return &BootstrapResult{
		CACert: caCertData,
		CAKey:  caKeyData,
	}, nil
}

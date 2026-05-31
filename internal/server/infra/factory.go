// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package infra

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/nstance-dev/nstance/internal/server/infra/aws"
	"github.com/nstance-dev/nstance/internal/server/infra/gcp"
	"github.com/nstance-dev/nstance/internal/server/infra/mock"
	"github.com/nstance-dev/nstance/internal/server/infra/provider"
	"github.com/nstance-dev/nstance/internal/server/infra/proxmox"
	"github.com/nstance-dev/nstance/internal/server/infra/tmux"
)

// HighestProviderIDFunc returns the highest known numeric provider instance ID
// from an external source (e.g. the local database). Used by providers that
// allocate numeric instance IDs to avoid reusing recently freed values.
type HighestProviderIDFunc func(ctx context.Context) (int64, error)

// ProviderOptions contains options for creating a provider
type ProviderOptions struct {
	Config            provider.ProviderConfig
	Logger            *slog.Logger
	Shard             string                // Shard ID (used by dev provider for tmux session naming)
	DevK8sDir         string                // Only used by dev provider - directory where dev-k8s stores resources
	RegistrationAddr  string                // Only used by dev provider - server registration address for agents
	AgentAddr         string                // Only used by dev provider - server agent RPC address for agents
	HighestProviderID HighestProviderIDFunc // Optional callback for providers that allocate numeric instance IDs
}

// NewProvider creates a Provider based on the configuration
func NewProvider(config provider.ProviderConfig, logger *slog.Logger) (provider.Provider, error) {
	return NewProviderWithOptions(ProviderOptions{
		Config: config,
		Logger: logger,
	})
}

// NewProviderWithOptions creates a Provider with full options
func NewProviderWithOptions(opts ProviderOptions) (provider.Provider, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	if err := validateConfig(opts.Config); err != nil {
		return nil, fmt.Errorf("invalid provider config: %w", err)
	}

	switch opts.Config.Kind {
	case "aws":
		return aws.NewProvider(aws.Options{
			Config: opts.Config,
			Logger: opts.Logger,
		})
	case "gcp":
		return gcp.NewProvider(gcp.Options{
			Config: opts.Config,
			Logger: opts.Logger,
		})

	case "proxmox":
		apiURL := os.Getenv("PROXMOX_API_URL")
		if apiURL == "" {
			apiURL = "https://localhost:8006/api2/json"
		}
		tokenID := os.Getenv("PROXMOX_TOKEN_ID")
		if tokenID == "" {
			return nil, fmt.Errorf("PROXMOX_TOKEN_ID environment variable is required for proxmox provider")
		}
		tokenSecret := os.Getenv("PROXMOX_TOKEN_SECRET")
		if tokenSecret == "" {
			return nil, fmt.Errorf("PROXMOX_TOKEN_SECRET environment variable is required for proxmox provider")
		}
		return proxmox.NewProvider(proxmox.Options{
			Config:            opts.Config,
			Logger:            opts.Logger,
			APIURL:            apiURL,
			TokenID:           tokenID,
			TokenSecret:       tokenSecret,
			VMIDHighWaterMark: opts.HighestProviderID,
		})
	case "tmux":
		return tmux.NewProvider(tmux.Options{
			Config:           opts.Config,
			Logger:           opts.Logger,
			Shard:            opts.Shard,
			SkipBinaryCheck:  false,
			DevK8sDir:        opts.DevK8sDir,
			RegistrationAddr: opts.RegistrationAddr,
			AgentAddr:        opts.AgentAddr,
		}), nil
	case "mock":
		return mock.NewProvider(mock.Options{
			Config: opts.Config,
			Logger: opts.Logger,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", opts.Config.Kind)
	}
}

func validateConfig(config provider.ProviderConfig) error {
	if config.Kind == "" {
		return fmt.Errorf("provider kind is required")
	}
	if config.Region == "" {
		return fmt.Errorf("provider region is required")
	}
	if config.Zone == "" {
		return fmt.Errorf("provider zone is required")
	}
	return nil
}

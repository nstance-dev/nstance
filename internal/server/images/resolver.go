// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package images

import (
	"context"
	"fmt"

	"github.com/nstance-dev/nstance/internal/server/infra"

	"github.com/nstance-dev/nstance/internal/server/config"
)

// Resolver interface supports multiple cloud providers
// Initial implementation: AWS only
// In future, other providers requiring this mechanism, such as Oracle Cloud, could be added
type Resolver interface {
	// Resolve looks up all configured images and returns a map of name -> image ID
	Resolve(ctx context.Context, configs map[string]config.ImageConfig) (map[string]string, error)

	// ResolveOne looks up a single image configuration
	ResolveOne(ctx context.Context, name string, cfg config.ImageConfig) (string, error)
}

// NewResolver creates a provider-specific resolver
func NewResolver(provider string, providerCfg infra.ProviderConfig) (Resolver, error) {
	switch provider {
	case "aws":
		return NewAWSResolver(providerCfg)
	default:
		return nil, fmt.Errorf("unsupported image resolver provider: %s", provider)
	}
}

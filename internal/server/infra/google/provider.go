// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	compute "cloud.google.com/go/compute/apiv1"
	computev1 "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// Provider implements both the provider.Provider and provider.LoadBalancerProvider interfaces for Google Cloud
type Provider struct {
	// For instance operations (using newer REST API)
	instancesClient *compute.InstancesClient
	subnetClient    *compute.SubnetworksClient

	// For load balancer operations (using older v1 API)
	computeService *computev1.Service

	config  provider.ProviderConfig
	logger  *slog.Logger
	options ProviderOptions
}

// ProviderOptions contains Google Cloud-specific configuration options
type ProviderOptions struct {
	ProjectID string `json:"project_id"`
}

// Options contains options for creating a Google Cloud provider
type Options struct {
	Config provider.ProviderConfig
	Logger *slog.Logger
}

// NewProvider creates a new unified Google Cloud provider that implements both instance and load balancer interfaces
func NewProvider(opts Options) (*Provider, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	var providerOpts ProviderOptions
	if opts.Config.Options != nil {
		optBytes, err := json.Marshal(opts.Config.Options)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Google Cloud options: %w", err)
		}
		if err := json.Unmarshal(optBytes, &providerOpts); err != nil {
			return nil, fmt.Errorf("invalid Google Cloud options: %w", err)
		}
	}

	if providerOpts.ProjectID == "" {
		return nil, fmt.Errorf("project_id is required for Google Cloud provider")
	}

	ctx := context.Background()

	// Create instances client for VM operations
	instancesClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create instances client: %w", err)
	}

	// Create subnet client for network operations
	subnetClient, err := compute.NewSubnetworksRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create subnet client: %w", err)
	}

	// Create compute service for load balancer operations
	computeService, err := computev1.NewService(ctx, option.WithScopes(computev1.ComputeScope))
	if err != nil {
		return nil, fmt.Errorf("failed to create compute service: %w", err)
	}

	return &Provider{
		instancesClient: instancesClient,
		subnetClient:    subnetClient,
		computeService:  computeService,
		config:          opts.Config,
		logger:          opts.Logger,
		options:         providerOpts,
	}, nil
}

func (p *Provider) Kind() string {
	return "google"
}

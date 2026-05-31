// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// Provider implements the provider.Provider and provider.LoadBalancerProvider interfaces for AWS
type Provider struct {
	ec2Client   *ec2.Client
	elbv2Client *elasticloadbalancingv2.Client
	config      provider.ProviderConfig
	logger      *slog.Logger
	options     ProviderOptions
}

// ProviderOptions contains AWS-specific configuration options
type ProviderOptions struct {
	Profile string `json:"profile"` // AWS profile name (optional)
}

// Options contains options for creating an AWS provider
type Options struct {
	Config provider.ProviderConfig
	Logger *slog.Logger
}

// NewProvider creates a new AWS provider with both EC2 and ELBv2 capabilities
func NewProvider(opts Options) (*Provider, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	var providerOpts ProviderOptions
	if opts.Config.Options != nil {
		optBytes, err := json.Marshal(opts.Config.Options)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal AWS options: %w", err)
		}
		if err := json.Unmarshal(optBytes, &providerOpts); err != nil {
			return nil, fmt.Errorf("invalid AWS options: %w", err)
		}
	}

	// Load AWS configuration
	awsConfig, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(opts.Config.Region),
		config.WithSharedConfigProfile(providerOpts.Profile),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create EC2 and ELBv2 clients
	ec2Client := ec2.NewFromConfig(awsConfig)
	elbv2Client := elasticloadbalancingv2.NewFromConfig(awsConfig)

	return &Provider{
		ec2Client:   ec2Client,
		elbv2Client: elbv2Client,
		config:      opts.Config,
		logger:      opts.Logger,
		options:     providerOpts,
	}, nil
}

func (p *Provider) Kind() string {
	return "aws"
}

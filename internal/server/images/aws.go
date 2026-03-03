// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package images

import (
	"context"
	"fmt"
	"sort"

	"github.com/nstance-dev/nstance/internal/server/infra"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/nstance-dev/nstance/internal/server/config"
)

// EC2API defines the interface for EC2 operations needed by AWSResolver
type EC2API interface {
	DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

// AWSResolver implements the Resolver interface for AWS EC2
type AWSResolver struct {
	client EC2API
	region string
}

// NewAWSResolver creates a new AWS image resolver
func NewAWSResolver(providerCfg infra.ProviderConfig) (*AWSResolver, error) {
	ctx := context.Background()

	// Extract profile from options if present
	var profile string
	if providerCfg.Options != nil {
		if p, ok := providerCfg.Options["profile"].(string); ok {
			profile = p
		}
	}

	// Load AWS configuration
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(providerCfg.Region),
		awsconfig.WithSharedConfigProfile(profile),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &AWSResolver{
		client: ec2.NewFromConfig(cfg),
		region: providerCfg.Region,
	}, nil
}

// NewAWSResolverWithClient creates a new AWS image resolver with a custom EC2 client (for testing)
func NewAWSResolverWithClient(client EC2API, region string) *AWSResolver {
	return &AWSResolver{
		client: client,
		region: region,
	}
}

// Resolve looks up all configured images and returns a map of name -> image ID
func (r *AWSResolver) Resolve(ctx context.Context, configs map[string]config.ImageConfig) (map[string]string, error) {
	result := make(map[string]string)

	for name, cfg := range configs {
		imageID, err := r.ResolveOne(ctx, name, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve image %s: %w", name, err)
		}
		result[name] = imageID
	}

	return result, nil
}

// ResolveOne looks up a single image configuration
func (r *AWSResolver) ResolveOne(ctx context.Context, name string, cfg config.ImageConfig) (string, error) {
	// Build EC2 filters from config
	var ec2Filters []types.Filter
	for _, filter := range cfg.Filters {
		ec2Filters = append(ec2Filters, types.Filter{
			Name:   aws.String(filter.Name),
			Values: filter.Values,
		})
	}

	// Query EC2 for images
	input := &ec2.DescribeImagesInput{
		Filters: ec2Filters,
		Owners:  cfg.Owners,
	}

	output, err := r.client.DescribeImages(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe images: %w", err)
	}

	if len(output.Images) == 0 {
		return "", fmt.Errorf("no images found matching filters")
	}

	// Sort images based on config
	images := output.Images
	switch cfg.Sort {
	case "creation-date":
		sort.Slice(images, func(i, j int) bool {
			if cfg.Order == "desc" {
				return aws.ToString(images[i].CreationDate) > aws.ToString(images[j].CreationDate)
			}
			return aws.ToString(images[i].CreationDate) < aws.ToString(images[j].CreationDate)
		})
	case "name":
		sort.Slice(images, func(i, j int) bool {
			if cfg.Order == "desc" {
				return aws.ToString(images[i].Name) > aws.ToString(images[j].Name)
			}
			return aws.ToString(images[i].Name) < aws.ToString(images[j].Name)
		})
	default:
		return "", fmt.Errorf("unsupported sort field: %s", cfg.Sort)
	}

	// Return the first image after sorting (latest based on order)
	return aws.ToString(images[0].ImageId), nil
}

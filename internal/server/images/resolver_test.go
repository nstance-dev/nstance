// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package images

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/infra"
)

// mockEC2Client implements a mock EC2 client for testing
type mockEC2Client struct {
	describeImagesFunc func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

func (m *mockEC2Client) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if m.describeImagesFunc != nil {
		return m.describeImagesFunc(ctx, params, optFns...)
	}
	return nil, nil
}

func TestNewResolver(t *testing.T) {
	t.Run("AWS provider", func(t *testing.T) {
		providerCfg := infra.ProviderConfig{
			Kind:   "aws",
			Region: "us-west-2",
		}
		resolver, err := NewResolver("aws", providerCfg)
		if err != nil {
			t.Fatalf("Failed to create AWS resolver: %v", err)
		}
		if resolver == nil {
			t.Error("Expected resolver to be created")
		}
		// Type assert to check it's an AWS resolver
		if _, ok := resolver.(*AWSResolver); !ok {
			t.Error("Expected AWSResolver type")
		}
	})

	t.Run("Unsupported provider", func(t *testing.T) {
		providerCfg := infra.ProviderConfig{
			Kind:   "unsupported",
			Region: "us-west-2",
		}
		resolver, err := NewResolver("unsupported", providerCfg)
		if err == nil {
			t.Error("Expected error for unsupported provider")
		}
		if resolver != nil {
			t.Error("Expected resolver to be nil for unsupported provider")
		}
	})
}

func TestAWSResolver_ResolveOne(t *testing.T) {
	ctx := context.Background()

	t.Run("Successful resolution with creation-date sort desc", func(t *testing.T) {
		mockClient := &mockEC2Client{
			describeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
				// Verify filters
				if len(params.Filters) != 2 {
					t.Errorf("Expected 2 filters, got %d", len(params.Filters))
				}
				expectedFilters := map[string][]string{
					"name":     {"ubuntu/images/hvm-ssd/ubuntu-jammy-*"},
					"owner-id": {"099720109477"},
				}
				for _, filter := range params.Filters {
					values, exists := expectedFilters[aws.ToString(filter.Name)]
					if !exists {
						t.Errorf("Unexpected filter: %s", aws.ToString(filter.Name))
					}
					if len(filter.Values) != len(values) || filter.Values[0] != values[0] {
						t.Errorf("Filter values mismatch for %s: expected %v, got %v", aws.ToString(filter.Name), values, filter.Values)
					}
				}

				return &ec2.DescribeImagesOutput{
					Images: []types.Image{
						{
							ImageId:      aws.String("ami-newer"),
							CreationDate: aws.String("2024-01-02T00:00:00Z"),
							Name:         aws.String("ubuntu-jammy-newer"),
						},
						{
							ImageId:      aws.String("ami-older"),
							CreationDate: aws.String("2024-01-01T00:00:00Z"),
							Name:         aws.String("ubuntu-jammy-older"),
						},
					},
				}, nil
			},
		}

		resolver := NewAWSResolverWithClient(mockClient, "us-west-2")

		cfg := config.ImageConfig{
			Provider: "aws",
			Filters: []config.ImageFilter{
				{Name: "name", Values: []string{"ubuntu/images/hvm-ssd/ubuntu-jammy-*"}},
				{Name: "owner-id", Values: []string{"099720109477"}},
			},
			Sort:  "creation-date",
			Order: "desc",
		}

		imageID, err := resolver.ResolveOne(ctx, "test-image", cfg)
		if err != nil {
			t.Fatalf("Failed to resolve image: %v", err)
		}
		if imageID != "ami-newer" {
			t.Errorf("Expected image ID 'ami-newer', got '%s'", imageID)
		}
	})

	t.Run("Successful resolution with name sort asc", func(t *testing.T) {
		mockClient := &mockEC2Client{
			describeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
				return &ec2.DescribeImagesOutput{
					Images: []types.Image{
						{
							ImageId: aws.String("ami-b"),
							Name:    aws.String("ubuntu-b"),
						},
						{
							ImageId: aws.String("ami-a"),
							Name:    aws.String("ubuntu-a"),
						},
					},
				}, nil
			},
		}

		resolver := NewAWSResolverWithClient(mockClient, "us-west-2")

		cfg := config.ImageConfig{
			Provider: "aws",
			Filters: []config.ImageFilter{
				{Name: "name", Values: []string{"ubuntu-*"}},
			},
			Sort:  "name",
			Order: "asc",
		}

		imageID, err := resolver.ResolveOne(ctx, "test-image", cfg)
		if err != nil {
			t.Fatalf("Failed to resolve image: %v", err)
		}
		if imageID != "ami-a" {
			t.Errorf("Expected image ID 'ami-a', got '%s'", imageID)
		}
	})

	t.Run("No images found", func(t *testing.T) {
		mockClient := &mockEC2Client{
			describeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
				return &ec2.DescribeImagesOutput{
					Images: []types.Image{},
				}, nil
			},
		}

		resolver := NewAWSResolverWithClient(mockClient, "us-west-2")

		cfg := config.ImageConfig{
			Provider: "aws",
			Filters: []config.ImageFilter{
				{Name: "name", Values: []string{"non-existent"}},
			},
			Sort:  "creation-date",
			Order: "desc",
		}

		_, err := resolver.ResolveOne(ctx, "test-image", cfg)
		if err == nil {
			t.Error("Expected error when no images found")
		}
	})

	t.Run("Unsupported sort field", func(t *testing.T) {
		mockClient := &mockEC2Client{
			describeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
				return &ec2.DescribeImagesOutput{
					Images: []types.Image{
						{ImageId: aws.String("ami-test")},
					},
				}, nil
			},
		}

		resolver := NewAWSResolverWithClient(mockClient, "us-west-2")

		cfg := config.ImageConfig{
			Provider: "aws",
			Filters: []config.ImageFilter{
				{Name: "name", Values: []string{"test"}},
			},
			Sort:  "unsupported",
			Order: "desc",
		}

		_, err := resolver.ResolveOne(ctx, "test-image", cfg)
		if err == nil {
			t.Error("Expected error for unsupported sort field")
		}
	})

	t.Run("EC2 error", func(t *testing.T) {
		mockClient := &mockEC2Client{
			describeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
				return nil, fmt.Errorf("EC2 error")
			},
		}

		resolver := NewAWSResolverWithClient(mockClient, "us-west-2")

		cfg := config.ImageConfig{
			Provider: "aws",
			Filters: []config.ImageFilter{
				{Name: "name", Values: []string{"test"}},
			},
			Sort:  "creation-date",
			Order: "desc",
		}

		_, err := resolver.ResolveOne(ctx, "test-image", cfg)
		if err == nil {
			t.Error("Expected error from EC2")
		}
	})
}

func TestAWSResolver_Resolve(t *testing.T) {
	ctx := context.Background()

	t.Run("Multiple images", func(t *testing.T) {
		callCount := 0
		mockClient := &mockEC2Client{
			describeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
				callCount++
				// Extract the filter value to determine which image to return
				var imageName string
				for _, filter := range params.Filters {
					if filter.Name != nil && *filter.Name == "name" && len(filter.Values) > 0 {
						imageName = filter.Values[0]
						break
					}
				}
				// Return an AMI based on the filter
				var amiID string
				switch imageName {
				case "image1-*":
					amiID = "ami-1"
				case "image2-*":
					amiID = "ami-2"
				default:
					amiID = "ami-unknown"
				}
				return &ec2.DescribeImagesOutput{
					Images: []types.Image{
						{
							ImageId: aws.String(amiID),
							Name:    aws.String(imageName),
						},
					},
				}, nil
			},
		}

		resolver := NewAWSResolverWithClient(mockClient, "us-west-2")

		configs := map[string]config.ImageConfig{
			"image1": {
				Provider: "aws",
				Filters: []config.ImageFilter{
					{Name: "name", Values: []string{"image1-*"}},
				},
				Sort:  "name",
				Order: "asc",
			},
			"image2": {
				Provider: "aws",
				Filters: []config.ImageFilter{
					{Name: "name", Values: []string{"image2-*"}},
				},
				Sort:  "name",
				Order: "asc",
			},
		}

		result, err := resolver.Resolve(ctx, configs)
		if err != nil {
			t.Fatalf("Failed to resolve images: %v", err)
		}

		if len(result) != 2 {
			t.Errorf("Expected 2 results, got %d", len(result))
		}
		if result["image1"] != "ami-1" {
			t.Errorf("Expected image1 to be 'ami-1', got '%s'", result["image1"])
		}
		if result["image2"] != "ami-2" {
			t.Errorf("Expected image2 to be 'ami-2', got '%s'", result["image2"])
		}
		if callCount != 2 {
			t.Errorf("Expected 2 EC2 calls, got %d", callCount)
		}
	})

	t.Run("Resolution error propagates", func(t *testing.T) {
		mockClient := &mockEC2Client{
			describeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
				return &ec2.DescribeImagesOutput{
					Images: []types.Image{},
				}, nil
			},
		}

		resolver := NewAWSResolverWithClient(mockClient, "us-west-2")

		configs := map[string]config.ImageConfig{
			"bad-image": {
				Provider: "aws",
				Filters: []config.ImageFilter{
					{Name: "name", Values: []string{"non-existent"}},
				},
				Sort:  "creation-date",
				Order: "desc",
			},
		}

		_, err := resolver.Resolve(ctx, configs)
		if err == nil {
			t.Error("Expected error from Resolve")
		}
	})
}

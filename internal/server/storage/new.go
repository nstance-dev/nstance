// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	googleStorage "cloud.google.com/go/storage"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// StorageOptions contains parameters for creating storage backends with full configuration.
type StorageOptions struct {
	Provider  string // Storage provider: "s3", "gcs", "file"
	Bucket    string // Bucket name (or path for file storage)
	Region    string // AWS region override (optional)
	Endpoint  string // Custom endpoint for S3-compatible backends (SeaweedFS, MinIO, Ceph RGW)
	PathStyle bool   // Use path-style access for S3 (default: false, uses virtual-hosted style)
}

// New creates the appropriate storage backend based on the provider string.
// Valid providers: "s3", "gcs", "file"
// Returns the storage, a cleanup function, and any error.
func New(ctx context.Context, logger *slog.Logger, provider, bucket string) (Storage, func(), error) {
	switch provider {
	case "gcs":
		logger.Info("Using GCS storage backend", "bucket", bucket)
		gcsClient, err := googleStorage.NewClient(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("create gcs client: %w", err)
		}
		return NewGCSStorage(gcsClient, bucket), func() { _ = gcsClient.Close() }, nil

	case "s3":
		logger.Info("Using S3 storage backend", "bucket", bucket)
		awsConfig, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("load aws config: %w", err)
		}
		s3Client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
			o.UsePathStyle = os.Getenv("AWS_S3_USE_PATH_STYLE") == "true"
			o.DisableLogOutputChecksumValidationSkipped = true
		})
		return NewS3Storage(s3Client, bucket), func() {}, nil

	case "file":
		logger.Info("Using file storage backend", "path", bucket)
		fileStorage, err := NewFileStorage(bucket)
		if err != nil {
			return nil, nil, fmt.Errorf("create file storage: %w", err)
		}
		return fileStorage, func() {}, nil

	default:
		return nil, nil, fmt.Errorf("unknown storage provider %q (expected s3|gcs|file)", provider)
	}
}

// NewWithOptions creates a storage backend with full configuration options.
// This supports custom endpoints for S3-compatible backends like SeaweedFS, MinIO, and Ceph RGW.
func NewWithOptions(ctx context.Context, logger *slog.Logger, opts StorageOptions) (Storage, func(), error) {
	switch opts.Provider {
	case "gcs":
		logger.Info("Using GCS storage backend", "bucket", opts.Bucket)
		gcsClient, err := googleStorage.NewClient(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("create gcs client: %w", err)
		}
		return NewGCSStorage(gcsClient, opts.Bucket), func() { _ = gcsClient.Close() }, nil

	case "s3":
		logFields := []any{"bucket", opts.Bucket}
		if opts.Endpoint != "" {
			logFields = append(logFields, "endpoint", opts.Endpoint)
		}
		if opts.Region != "" {
			logFields = append(logFields, "region", opts.Region)
		}
		logger.Info("Using S3 storage backend", logFields...)

		var configOpts []func(*config.LoadOptions) error
		if opts.Region != "" {
			configOpts = append(configOpts, config.WithRegion(opts.Region))
		}

		awsConfig, err := config.LoadDefaultConfig(ctx, configOpts...)
		if err != nil {
			return nil, nil, fmt.Errorf("load aws config: %w", err)
		}

		s3Client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
			if opts.Endpoint != "" {
				o.BaseEndpoint = aws.String(opts.Endpoint)
			}
			if opts.PathStyle || os.Getenv("AWS_S3_USE_PATH_STYLE") == "true" {
				o.UsePathStyle = true
			}
			o.DisableLogOutputChecksumValidationSkipped = true
		})
		return NewS3Storage(s3Client, opts.Bucket), func() {}, nil

	case "file":
		logger.Info("Using file storage backend", "path", opts.Bucket)
		fileStorage, err := NewFileStorage(opts.Bucket)
		if err != nil {
			return nil, nil, fmt.Errorf("create file storage: %w", err)
		}
		return fileStorage, func() {}, nil

	default:
		return nil, nil, fmt.Errorf("unknown storage provider %q (expected s3|gcs|file)", opts.Provider)
	}
}

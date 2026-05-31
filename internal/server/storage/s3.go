// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Storage implements Storage interface using AWS S3
type S3Storage struct {
	client *s3.Client
	bucket string
}

// NewS3Storage creates a new S3 storage instance
func NewS3Storage(client *s3.Client, bucket string) *S3Storage {
	return &S3Storage{
		client: client,
		bucket: bucket,
	}
}

// Get retrieves an object from S3 and returns its contents and ETag
func (s *S3Storage) Get(ctx context.Context, key string) ([]byte, string, error) {
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("failed to get object %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read object body: %w", err)
	}

	etag := ""
	if resp.ETag != nil {
		etag = aws.ToString(resp.ETag)
		// S3 ETags are quoted, remove quotes
		etag = strings.Trim(etag, "\"")
	}

	return data, etag, nil
}

// Put stores an object in S3
func (s *S3Storage) Put(ctx context.Context, key string, data []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("failed to put object %s: %w", key, err)
	}
	return nil
}

// PutIfMatch stores an object only if the ETag matches (optimistic locking)
// Empty etag means object must not exist
func (s *S3Storage) PutIfMatch(ctx context.Context, key string, data []byte, etag string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}

	if etag == "" {
		// Object must not exist - use IfNoneMatch: *
		input.IfNoneMatch = aws.String("*")
	} else {
		// Object must have this ETag
		input.IfMatch = aws.String(etag)
	}

	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		// Check if it's a precondition failure
		if strings.Contains(err.Error(), "PreconditionFailed") || strings.Contains(err.Error(), "412") {
			return ErrPrecondition
		}
		return fmt.Errorf("failed to put object %s: %w", key, err)
	}
	return nil
}

// Delete removes an object from S3
func (s *S3Storage) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete object %s: %w", key, err)
	}
	return nil
}

// Exists checks if an object exists in S3
func (s *S3Storage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		var nsb *types.NotFound
		if errors.As(err, &nsk) || errors.As(err, &nsb) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check object existence %s: %w", key, err)
	}
	return true, nil
}

// List lists objects with the given prefix
func (s *S3Storage) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		resp, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects with prefix %s: %w", prefix, err)
		}

		for _, obj := range resp.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
	}

	return keys, nil
}

// GetMetadata retrieves metadata for an object
func (s *S3Storage) GetMetadata(ctx context.Context, key string) (*ObjectMetadata, error) {
	resp, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		var nsb *types.NotFound
		if errors.As(err, &nsk) || errors.As(err, &nsb) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get object metadata %s: %w", key, err)
	}

	metadata := &ObjectMetadata{
		Key:  key,
		Size: aws.ToInt64(resp.ContentLength),
	}

	if resp.LastModified != nil {
		metadata.LastModified = *resp.LastModified
	}

	if resp.ETag != nil {
		// Remove quotes from ETag
		metadata.ETag = strings.Trim(*resp.ETag, `"`)
	}

	if resp.ContentType != nil {
		metadata.ContentType = *resp.ContentType
	}

	return metadata, nil
}

// GetStream retrieves an object as a stream
func (s *S3Storage) GetStream(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get object stream %s: %w", key, err)
	}

	return resp.Body, nil
}

// PutStream stores an object from a stream
func (s *S3Storage) PutStream(ctx context.Context, key string, reader io.Reader, size int64) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   reader,
	}

	if size > 0 {
		input.ContentLength = aws.Int64(size)
	}

	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to put object stream %s: %w", key, err)
	}
	return nil
}

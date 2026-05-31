// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
)

// isPreconditionFailed checks if an error is a 412 Precondition Failed
func isPreconditionFailed(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == 412
}

// GCSStorage implements Storage interface using Google Cloud Storage
type GCSStorage struct {
	client *storage.Client
	bucket string
}

// NewGCSStorage creates a new GCS storage instance
func NewGCSStorage(client *storage.Client, bucket string) *GCSStorage {
	return &GCSStorage{
		client: client,
		bucket: bucket,
	}
}

// Get retrieves an object from GCS and returns its contents and ETag (Generation)
func (g *GCSStorage) Get(ctx context.Context, key string) ([]byte, string, error) {
	obj := g.client.Bucket(g.bucket).Object(key)
	reader, err := obj.NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("failed to get object %s: %w", key, err)
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read object body: %w", err)
	}

	etag := fmt.Sprintf("%d", reader.Attrs.Generation)

	return data, etag, nil
}

// Put stores an object in GCS
func (g *GCSStorage) Put(ctx context.Context, key string, data []byte) error {
	obj := g.client.Bucket(g.bucket).Object(key)
	writer := obj.NewWriter(ctx)

	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return fmt.Errorf("failed to write object %s: %w", key, err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close writer for object %s: %w", key, err)
	}

	return nil
}

// PutIfMatch stores an object only if the generation matches (optimistic locking)
// Empty etag means object must not exist
func (g *GCSStorage) PutIfMatch(ctx context.Context, key string, data []byte, etag string) error {
	obj := g.client.Bucket(g.bucket).Object(key)

	var conds storage.Conditions
	if etag == "" {
		conds.DoesNotExist = true
	} else {
		var generation int64
		if _, err := fmt.Sscanf(etag, "%d", &generation); err != nil {
			return fmt.Errorf("invalid etag format: %w", err)
		}
		conds.GenerationMatch = generation
	}

	writer := obj.If(conds).NewWriter(ctx)

	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		if errors.Is(err, storage.ErrObjectNotExist) {
			return ErrPrecondition
		}
		return fmt.Errorf("failed to write object %s: %w", key, err)
	}

	if err := writer.Close(); err != nil {
		if isPreconditionFailed(err) {
			return ErrPrecondition
		}
		return fmt.Errorf("failed to close writer for object %s: %w", key, err)
	}

	return nil
}

// Delete removes an object from GCS
func (g *GCSStorage) Delete(ctx context.Context, key string) error {
	obj := g.client.Bucket(g.bucket).Object(key)
	if err := obj.Delete(ctx); err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil
		}
		return fmt.Errorf("failed to delete object %s: %w", key, err)
	}
	return nil
}

// Exists checks if an object exists in GCS
func (g *GCSStorage) Exists(ctx context.Context, key string) (bool, error) {
	obj := g.client.Bucket(g.bucket).Object(key)
	_, err := obj.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check object existence %s: %w", key, err)
	}
	return true, nil
}

// List lists objects with the given prefix
func (g *GCSStorage) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	it := g.client.Bucket(g.bucket).Objects(ctx, &storage.Query{Prefix: prefix})

	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list objects with prefix %s: %w", prefix, err)
		}
		keys = append(keys, attrs.Name)
	}

	return keys, nil
}

// GetMetadata retrieves metadata for an object
func (g *GCSStorage) GetMetadata(ctx context.Context, key string) (*ObjectMetadata, error) {
	obj := g.client.Bucket(g.bucket).Object(key)
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get object metadata %s: %w", key, err)
	}

	return &ObjectMetadata{
		Key:          key,
		Size:         attrs.Size,
		LastModified: attrs.Updated,
		ETag:         fmt.Sprintf("%d", attrs.Generation),
		ContentType:  attrs.ContentType,
	}, nil
}

// GetStream retrieves an object as a stream
func (g *GCSStorage) GetStream(ctx context.Context, key string) (io.ReadCloser, error) {
	obj := g.client.Bucket(g.bucket).Object(key)
	reader, err := obj.NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get object stream %s: %w", key, err)
	}
	return reader, nil
}

// PutStream stores an object from a stream
func (g *GCSStorage) PutStream(ctx context.Context, key string, reader io.Reader, size int64) error {
	obj := g.client.Bucket(g.bucket).Object(key)
	writer := obj.NewWriter(ctx)

	if size > 0 {
		writer.Size = size
	}

	if _, err := io.Copy(writer, reader); err != nil {
		_ = writer.Close()
		return fmt.Errorf("failed to put object stream %s: %w", key, err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close writer for object stream %s: %w", key, err)
	}

	return nil
}

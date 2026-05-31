// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"io"
	"time"

	"github.com/nadrama-com/s3lect"
)

// Common storage errors - use s3lect's errors for compatibility
var (
	ErrNotFound     = s3lect.ErrStorageNotFound
	ErrPrecondition = s3lect.ErrStoragePrecondition
)

// Storage provides an abstraction for object storage operations
type Storage interface {
	// Get retrieves an object from storage and returns its contents and its etag
	// Returns ErrNotFound if object does not exist
	Get(ctx context.Context, key string) ([]byte, string, error)

	// Put stores an object in storage
	Put(ctx context.Context, key string, data []byte) error

	// PutIfMatch stores an object only if the ETag matches (empty string = must not exist)
	// Returns ErrPrecondition if ETag doesn't match
	PutIfMatch(ctx context.Context, key string, data []byte, etag string) error

	// Delete removes an object from storage
	Delete(ctx context.Context, key string) error

	// Exists checks if an object exists in storage
	Exists(ctx context.Context, key string) (bool, error)

	// List lists objects with the given prefix
	List(ctx context.Context, prefix string) ([]string, error)

	// GetMetadata retrieves metadata for an object
	GetMetadata(ctx context.Context, key string) (*ObjectMetadata, error)

	// GetStream retrieves an object as a stream
	GetStream(ctx context.Context, key string) (io.ReadCloser, error)

	// PutStream stores an object from a stream
	PutStream(ctx context.Context, key string, reader io.Reader, size int64) error
}

// ObjectMetadata contains metadata about a stored object
type ObjectMetadata struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	ContentType  string
}

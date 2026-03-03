// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"io"
	"strings"
)

// ScopedStorage wraps a Storage interface and prefixes all keys with a given prefix.
// This enables logical separation of cluster-scoped and shard-scoped data within a single bucket.
type ScopedStorage struct {
	underlying Storage
	prefix     string
}

// NewScopedStorage creates a new ScopedStorage that prefixes all keys.
// The prefix should include a trailing slash if keys should be nested (e.g., "cluster/").
func NewScopedStorage(underlying Storage, prefix string) Storage {
	return &ScopedStorage{
		underlying: underlying,
		prefix:     prefix,
	}
}

// Prefix returns the configured prefix for this scoped storage
func (s *ScopedStorage) Prefix() string {
	return s.prefix
}

// prefixKey adds the configured prefix to the key
func (s *ScopedStorage) prefixKey(key string) string {
	return s.prefix + key
}

// stripPrefix removes the configured prefix from a key
func (s *ScopedStorage) stripPrefix(key string) string {
	return strings.TrimPrefix(key, s.prefix)
}

// Get retrieves an object from storage and returns its contents and its etag
func (s *ScopedStorage) Get(ctx context.Context, key string) ([]byte, string, error) {
	return s.underlying.Get(ctx, s.prefixKey(key))
}

// Put stores an object in storage
func (s *ScopedStorage) Put(ctx context.Context, key string, data []byte) error {
	return s.underlying.Put(ctx, s.prefixKey(key), data)
}

// PutIfMatch stores an object only if the ETag matches (empty string = must not exist)
func (s *ScopedStorage) PutIfMatch(ctx context.Context, key string, data []byte, etag string) error {
	return s.underlying.PutIfMatch(ctx, s.prefixKey(key), data, etag)
}

// Delete removes an object from storage
func (s *ScopedStorage) Delete(ctx context.Context, key string) error {
	return s.underlying.Delete(ctx, s.prefixKey(key))
}

// Exists checks if an object exists in storage
func (s *ScopedStorage) Exists(ctx context.Context, key string) (bool, error) {
	return s.underlying.Exists(ctx, s.prefixKey(key))
}

// List lists objects with the given prefix, stripping the scope prefix from returned keys
func (s *ScopedStorage) List(ctx context.Context, prefix string) ([]string, error) {
	keys, err := s.underlying.List(ctx, s.prefixKey(prefix))
	if err != nil {
		return nil, err
	}

	result := make([]string, len(keys))
	for i, key := range keys {
		result[i] = s.stripPrefix(key)
	}
	return result, nil
}

// GetMetadata retrieves metadata for an object, updating the Key field to strip the prefix
func (s *ScopedStorage) GetMetadata(ctx context.Context, key string) (*ObjectMetadata, error) {
	meta, err := s.underlying.GetMetadata(ctx, s.prefixKey(key))
	if err != nil {
		return nil, err
	}

	meta.Key = s.stripPrefix(meta.Key)
	return meta, nil
}

// GetStream retrieves an object as a stream
func (s *ScopedStorage) GetStream(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.underlying.GetStream(ctx, s.prefixKey(key))
}

// PutStream stores an object from a stream
func (s *ScopedStorage) PutStream(ctx context.Context, key string, reader io.Reader, size int64) error {
	return s.underlying.PutStream(ctx, s.prefixKey(key), reader, size)
}

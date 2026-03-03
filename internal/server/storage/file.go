// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileStorage implements Storage interface for local filesystem operations
type FileStorage struct {
	baseDir string
}

// NewFileStorage creates a new file-based storage
func NewFileStorage(baseDir string) (*FileStorage, error) {
	// Create base directory if it doesn't exist
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	return &FileStorage{
		baseDir: baseDir,
	}, nil
}

// Get retrieves data from a file and returns its contents and ETag
func (f *FileStorage) Get(ctx context.Context, key string) ([]byte, string, error) {
	if err := f.validateKey(key); err != nil {
		return nil, "", err
	}

	filePath := filepath.Join(f.baseDir, key)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("failed to read file: %w", err)
	}

	// Compute ETag as MD5 hash
	etag := fmt.Sprintf("%x", md5.Sum(data))

	return data, etag, nil
}

// Put stores data to a file
func (f *FileStorage) Put(ctx context.Context, key string, data []byte) error {
	if err := f.validateKey(key); err != nil {
		return err
	}

	filePath := filepath.Join(f.baseDir, key)

	// Create directory if it doesn't exist
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// PutIfMatch stores data only if ETag matches (optimistic locking)
// Empty etag means file must not exist
func (f *FileStorage) PutIfMatch(ctx context.Context, key string, data []byte, etag string) error {
	if err := f.validateKey(key); err != nil {
		return err
	}

	filePath := filepath.Join(f.baseDir, key)

	// Check current state
	currentMeta, err := f.GetMetadata(ctx, key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("failed to get current metadata: %w", err)
	}

	currentETag := ""
	if currentMeta != nil {
		currentETag = currentMeta.ETag
	}

	// Validate precondition
	if etag == "" {
		// Must not exist
		if currentETag != "" {
			return ErrPrecondition
		}
	} else {
		// Must match
		if currentETag != etag {
			return ErrPrecondition
		}
	}

	// Write file
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// Delete removes a file
func (f *FileStorage) Delete(ctx context.Context, key string) error {
	if err := f.validateKey(key); err != nil {
		return err
	}

	filePath := filepath.Join(f.baseDir, key)
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("key not found: %s", key)
		}
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// Exists checks if a file exists
func (f *FileStorage) Exists(ctx context.Context, key string) (bool, error) {
	if err := f.validateKey(key); err != nil {
		return false, err
	}

	filePath := filepath.Join(f.baseDir, key)
	_, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check file existence: %w", err)
	}

	return true, nil
}

// List lists files with the given prefix
func (f *FileStorage) List(ctx context.Context, prefix string) ([]string, error) {
	if err := f.validateKey(prefix); err != nil {
		return nil, err
	}

	var keys []string

	err := filepath.Walk(f.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Get relative path from base directory
		relPath, err := filepath.Rel(f.baseDir, path)
		if err != nil {
			return err
		}

		// Convert to forward slashes for consistency
		relPath = filepath.ToSlash(relPath)

		// Check if it matches the prefix
		if strings.HasPrefix(relPath, prefix) {
			keys = append(keys, relPath)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	return keys, nil
}

// GetMetadata retrieves file metadata
func (f *FileStorage) GetMetadata(ctx context.Context, key string) (*ObjectMetadata, error) {
	if err := f.validateKey(key); err != nil {
		return nil, err
	}

	filePath := filepath.Join(f.baseDir, key)
	stat, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get file metadata: %w", err)
	}

	// Read file to compute MD5 hash (matching S3 ETag behavior)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file for ETag: %w", err)
	}
	etag := fmt.Sprintf("%x", md5.Sum(data))

	return &ObjectMetadata{
		Key:          key,
		Size:         stat.Size(),
		LastModified: stat.ModTime(),
		ETag:         etag,
		ContentType:  "application/octet-stream",
	}, nil
}

// GetStream retrieves a file as a stream
func (f *FileStorage) GetStream(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := f.validateKey(key); err != nil {
		return nil, err
	}

	filePath := filepath.Join(f.baseDir, key)
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	return file, nil
}

// PutStream stores a file from a stream
func (f *FileStorage) PutStream(ctx context.Context, key string, reader io.Reader, size int64) error {
	if err := f.validateKey(key); err != nil {
		return err
	}

	filePath := filepath.Join(f.baseDir, key)

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer func() { _ = file.Close() }()

	if _, err := io.Copy(file, reader); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// validateKey ensures the key doesn't contain path traversal attempts
func (f *FileStorage) validateKey(key string) error {
	if strings.Contains(key, "..") {
		return fmt.Errorf("invalid key: path traversal not allowed")
	}

	// Convert to clean path and ensure it's relative
	cleaned := filepath.Clean(key)
	if filepath.IsAbs(cleaned) {
		return fmt.Errorf("invalid key: absolute paths not allowed")
	}

	return nil
}

// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MockStorage implements Storage interface using local filesystem for testing
type MockStorage struct {
	baseDir string
	mu      sync.RWMutex
}

// NewMockStorage creates a new mock storage instance
func NewMockStorage(baseDir string) (*MockStorage, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	return &MockStorage{
		baseDir: baseDir,
	}, nil
}

// NewMock creates a new mock storage instance with a temporary directory
func NewMock() *MockStorage {
	tempDir, err := os.MkdirTemp("", "mock-storage-")
	if err != nil {
		panic(fmt.Sprintf("failed to create temp dir for mock storage: %v", err))
	}

	storage, err := NewMockStorage(tempDir)
	if err != nil {
		panic(fmt.Sprintf("failed to create mock storage: %v", err))
	}

	return storage
}

// Get retrieves an object from mock storage and returns its contents and ETag
func (m *MockStorage) Get(ctx context.Context, key string) ([]byte, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filePath := m.keyToPath(key)
	data, err := os.ReadFile(filePath)
	if os.IsNotExist(err) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}

	// Compute ETag as MD5 hash
	etag := fmt.Sprintf("%x", md5.Sum(data))
	return data, etag, nil
}

// Put stores an object in mock storage
func (m *MockStorage) Put(ctx context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filePath := m.keyToPath(key)
	dir := filepath.Dir(filePath)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	return os.WriteFile(filePath, data, 0644)
}

// PutIfMatch stores an object only if the ETag matches (optimistic locking)
// Empty etag means object must not exist
func (m *MockStorage) PutIfMatch(ctx context.Context, key string, data []byte, etag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filePath := m.keyToPath(key)

	// Check current ETag
	currentETag := ""
	if _, err := os.Stat(filePath); err == nil {
		// File exists - get its ETag
		currentData, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read existing file: %w", err)
		}
		currentETag = fmt.Sprintf("%x", md5.Sum(currentData))
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

	return os.WriteFile(filePath, data, 0644)
}

// Delete removes an object from mock storage
func (m *MockStorage) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filePath := m.keyToPath(key)
	err := os.Remove(filePath)
	if os.IsNotExist(err) {
		return nil // Already deleted
	}
	return err
}

// Exists checks if an object exists in mock storage
func (m *MockStorage) Exists(ctx context.Context, key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filePath := m.keyToPath(key)
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// List lists objects with the given prefix
func (m *MockStorage) List(ctx context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var keys []string
	prefixPath := m.keyToPath(prefix)

	err := filepath.Walk(m.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(m.baseDir, path)
		if err != nil {
			return err
		}

		key := strings.ReplaceAll(relPath, string(filepath.Separator), "/")
		if strings.HasPrefix(key, prefix) || strings.HasPrefix(path, prefixPath) {
			keys = append(keys, key)
		}

		return nil
	})

	return keys, err
}

// GetMetadata retrieves metadata for an object
func (m *MockStorage) GetMetadata(ctx context.Context, key string) (*ObjectMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filePath := m.keyToPath(key)
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// Calculate ETag (MD5 for simplicity)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	etag := fmt.Sprintf("%x", md5.Sum(data))

	return &ObjectMetadata{
		Key:          key,
		Size:         info.Size(),
		LastModified: info.ModTime(),
		ETag:         etag,
		ContentType:  "application/octet-stream",
	}, nil
}

// GetStream retrieves an object as a stream
func (m *MockStorage) GetStream(ctx context.Context, key string) (io.ReadCloser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filePath := m.keyToPath(key)
	file, err := os.Open(filePath)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return file, err
}

// PutStream stores an object from a stream
func (m *MockStorage) PutStream(ctx context.Context, key string, reader io.Reader, size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filePath := m.keyToPath(key)
	dir := filepath.Dir(filePath)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = io.Copy(file, reader)
	return err
}

// keyToPath converts a storage key to a filesystem path
func (m *MockStorage) keyToPath(key string) string {
	// Replace forward slashes with OS-specific path separator
	parts := strings.Split(key, "/")
	return filepath.Join(append([]string{m.baseDir}, parts...)...)
}

// Cleanup removes all files in the mock storage (useful for testing)
func (m *MockStorage) Cleanup() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return os.RemoveAll(m.baseDir)
}

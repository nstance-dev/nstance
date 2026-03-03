// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStorage(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "file-storage-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	storage, err := NewFileStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to create file storage: %v", err)
	}

	ctx := context.Background()

	t.Run("PutAndGet", func(t *testing.T) {
		key := "test/file.txt"
		data := []byte("test data")

		// Put data
		err := storage.Put(ctx, key, data)
		if err != nil {
			t.Fatalf("Failed to put data: %v", err)
		}

		// Get data
		retrieved, _, err := storage.Get(ctx, key)
		if err != nil {
			t.Fatalf("Failed to get data: %v", err)
		}

		if string(retrieved) != string(data) {
			t.Errorf("Retrieved data doesn't match. Expected: %s, Got: %s", data, retrieved)
		}
	})

	t.Run("Exists", func(t *testing.T) {
		key := "test/exists.txt"
		data := []byte("exists test")

		// Should not exist initially
		exists, err := storage.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check existence: %v", err)
		}
		if exists {
			t.Error("File should not exist initially")
		}

		// Put data
		err = storage.Put(ctx, key, data)
		if err != nil {
			t.Fatalf("Failed to put data: %v", err)
		}

		// Should exist now
		exists, err = storage.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check existence: %v", err)
		}
		if !exists {
			t.Error("File should exist after putting data")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		key := "test/delete.txt"
		data := []byte("delete test")

		// Put data
		err := storage.Put(ctx, key, data)
		if err != nil {
			t.Fatalf("Failed to put data: %v", err)
		}

		// Delete data
		err = storage.Delete(ctx, key)
		if err != nil {
			t.Fatalf("Failed to delete data: %v", err)
		}

		// Should not exist after deletion
		exists, err := storage.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check existence: %v", err)
		}
		if exists {
			t.Error("File should not exist after deletion")
		}
	})

	t.Run("GetMetadata", func(t *testing.T) {
		key := "test/metadata.txt"
		data := []byte("metadata test")

		// Put data
		err := storage.Put(ctx, key, data)
		if err != nil {
			t.Fatalf("Failed to put data: %v", err)
		}

		// Get metadata
		metadata, err := storage.GetMetadata(ctx, key)
		if err != nil {
			t.Fatalf("Failed to get metadata: %v", err)
		}

		if metadata.Key != key {
			t.Errorf("Wrong key in metadata. Expected: %s, Got: %s", key, metadata.Key)
		}
		if metadata.Size != int64(len(data)) {
			t.Errorf("Wrong size in metadata. Expected: %d, Got: %d", len(data), metadata.Size)
		}
		if metadata.ETag == "" {
			t.Error("ETag should not be empty")
		}
		if time.Since(metadata.LastModified) > time.Minute {
			t.Error("LastModified should be recent")
		}
	})

	t.Run("List", func(t *testing.T) {
		// Put multiple files
		files := map[string][]byte{
			"test/list/file1.txt":  []byte("file1"),
			"test/list/file2.txt":  []byte("file2"),
			"test/other/file3.txt": []byte("file3"),
		}

		for key, data := range files {
			err := storage.Put(ctx, key, data)
			if err != nil {
				t.Fatalf("Failed to put file %s: %v", key, err)
			}
		}

		// List files with prefix
		keys, err := storage.List(ctx, "test/list/")
		if err != nil {
			t.Fatalf("Failed to list files: %v", err)
		}

		if len(keys) != 2 {
			t.Errorf("Expected 2 files, got %d", len(keys))
		}

		expectedKeys := []string{"test/list/file1.txt", "test/list/file2.txt"}
		for _, expected := range expectedKeys {
			found := false
			for _, key := range keys {
				if key == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected key %s not found in list", expected)
			}
		}
	})

	t.Run("GetNonExistentFile", func(t *testing.T) {
		_, _, err := storage.Get(ctx, "nonexistent/file.txt")
		if err == nil {
			t.Error("Expected error when getting non-existent file")
		}
	})

	t.Run("DeleteNonExistentFile", func(t *testing.T) {
		err := storage.Delete(ctx, "nonexistent/file.txt")
		if err == nil {
			t.Error("Expected error when deleting non-existent file")
		}
	})

	t.Run("InvalidKeys", func(t *testing.T) {
		invalidKeys := []string{
			"../etc/passwd",
			"/absolute/path",
			"path/../traversal",
		}

		for _, key := range invalidKeys {
			err := storage.Put(ctx, key, []byte("test"))
			if err == nil {
				t.Errorf("Expected error for invalid key: %s", key)
			}
		}
	})
}

func TestNewFileStorageCreatesDirectory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "file-storage-create-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Use a non-existent subdirectory
	storageDir := filepath.Join(tempDir, "new", "directory")

	storage, err := NewFileStorage(storageDir)
	if err != nil {
		t.Fatalf("Failed to create file storage: %v", err)
	}

	// Check that directory was created
	stat, err := os.Stat(storageDir)
	if err != nil {
		t.Fatalf("Storage directory was not created: %v", err)
	}
	if !stat.IsDir() {
		t.Error("Storage path is not a directory")
	}

	// Test that we can use the storage
	ctx := context.Background()
	err = storage.Put(ctx, "test.txt", []byte("test"))
	if err != nil {
		t.Fatalf("Failed to use newly created storage: %v", err)
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMockStorage(t *testing.T) {
	// Create temp directory for testing
	tempDir := filepath.Join(os.TempDir(), "nstance-storage-test")
	defer func() { _ = os.RemoveAll(tempDir) }()

	storage, err := NewMockStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to create mock storage: %v", err)
	}

	ctx := context.Background()

	t.Run("Put and Get", func(t *testing.T) {
		key := "test/object.json"
		data := []byte(`{"test": "data"}`)

		// Test Put
		err := storage.Put(ctx, key, data)
		if err != nil {
			t.Fatalf("Failed to put object: %v", err)
		}

		// Test Get
		retrieved, _, err := storage.Get(ctx, key)
		if err != nil {
			t.Fatalf("Failed to get object: %v", err)
		}

		if string(retrieved) != string(data) {
			t.Errorf("Retrieved data doesn't match. Expected: %s, Got: %s", string(data), string(retrieved))
		}
	})

	t.Run("Exists", func(t *testing.T) {
		key := "test/exists.json"
		data := []byte(`{"exists": true}`)

		// Should not exist initially
		exists, err := storage.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check existence: %v", err)
		}
		if exists {
			t.Error("Object should not exist initially")
		}

		// Put object
		err = storage.Put(ctx, key, data)
		if err != nil {
			t.Fatalf("Failed to put object: %v", err)
		}

		// Should exist now
		exists, err = storage.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check existence: %v", err)
		}
		if !exists {
			t.Error("Object should exist after putting")
		}
	})

	t.Run("List", func(t *testing.T) {
		// Put some objects
		objects := map[string][]byte{
			"list/test1.json":  []byte(`{"test": 1}`),
			"list/test2.json":  []byte(`{"test": 2}`),
			"other/test3.json": []byte(`{"test": 3}`),
		}

		for key, data := range objects {
			err := storage.Put(ctx, key, data)
			if err != nil {
				t.Fatalf("Failed to put object %s: %v", key, err)
			}
		}

		// List with prefix
		keys, err := storage.List(ctx, "list/")
		if err != nil {
			t.Fatalf("Failed to list objects: %v", err)
		}

		if len(keys) != 2 {
			t.Errorf("Expected 2 objects with prefix 'list/', got %d", len(keys))
		}

		// Check that both expected keys are present
		found := make(map[string]bool)
		for _, key := range keys {
			found[key] = true
		}

		if !found["list/test1.json"] || !found["list/test2.json"] {
			t.Errorf("Expected keys not found. Got: %v", keys)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		key := "test/delete.json"
		data := []byte(`{"delete": true}`)

		// Put object
		err := storage.Put(ctx, key, data)
		if err != nil {
			t.Fatalf("Failed to put object: %v", err)
		}

		// Verify it exists
		exists, err := storage.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check existence: %v", err)
		}
		if !exists {
			t.Error("Object should exist before deletion")
		}

		// Delete object
		err = storage.Delete(ctx, key)
		if err != nil {
			t.Fatalf("Failed to delete object: %v", err)
		}

		// Verify it doesn't exist
		exists, err = storage.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check existence after deletion: %v", err)
		}
		if exists {
			t.Error("Object should not exist after deletion")
		}

		// Delete again should not error
		err = storage.Delete(ctx, key)
		if err != nil {
			t.Fatalf("Second delete should not error: %v", err)
		}
	})

	t.Run("GetMetadata", func(t *testing.T) {
		key := "test/metadata.json"
		data := []byte(`{"metadata": "test"}`)

		// Put object
		err := storage.Put(ctx, key, data)
		if err != nil {
			t.Fatalf("Failed to put object: %v", err)
		}

		// Get metadata
		metadata, err := storage.GetMetadata(ctx, key)
		if err != nil {
			t.Fatalf("Failed to get metadata: %v", err)
		}

		if metadata.Key != key {
			t.Errorf("Expected key %s, got %s", key, metadata.Key)
		}

		if metadata.Size != int64(len(data)) {
			t.Errorf("Expected size %d, got %d", len(data), metadata.Size)
		}

		if metadata.ETag == "" {
			t.Error("ETag should not be empty")
		}

		if time.Since(metadata.LastModified) > time.Minute {
			t.Error("LastModified should be recent")
		}
	})

	t.Run("GetNonExistentObject", func(t *testing.T) {
		_, _, err := storage.Get(ctx, "nonexistent/object.json")
		if err == nil {
			t.Error("Expected error when getting nonexistent object")
		}
	})

	t.Run("GetMetadataNonExistent", func(t *testing.T) {
		_, err := storage.GetMetadata(ctx, "nonexistent/object.json")
		if err == nil {
			t.Error("Expected error when getting metadata for nonexistent object")
		}
	})
}

func TestMockStorageStreaming(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "nstance-stream-test")
	defer func() { _ = os.RemoveAll(tempDir) }()

	storage, err := NewMockStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to create mock storage: %v", err)
	}

	ctx := context.Background()

	t.Run("PutStream and GetStream", func(t *testing.T) {
		key := "test/stream.json"
		data := []byte(`{"stream": "test data"}`)

		// Test PutStream
		err := storage.PutStream(ctx, key, strings.NewReader(string(data)), int64(len(data)))
		if err != nil {
			t.Fatalf("Failed to put stream: %v", err)
		}

		// Test GetStream
		reader, err := storage.GetStream(ctx, key)
		if err != nil {
			t.Fatalf("Failed to get stream: %v", err)
		}
		defer func() { _ = reader.Close() }()

		retrieved := make([]byte, len(data))
		n, err := reader.Read(retrieved)
		if err != nil && err.Error() != "EOF" {
			t.Fatalf("Failed to read stream: %v", err)
		}

		if n != len(data) {
			t.Errorf("Expected to read %d bytes, got %d", len(data), n)
		}

		if string(retrieved) != string(data) {
			t.Errorf("Retrieved data doesn't match. Expected: %s, Got: %s", string(data), string(retrieved))
		}
	})
}

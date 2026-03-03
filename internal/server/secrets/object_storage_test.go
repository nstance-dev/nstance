// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nstance-dev/nstance/internal/server/storage"
)

func TestObjectStorageStore(t *testing.T) {
	// Create temp directory for mock storage
	tempDir := filepath.Join(os.TempDir(), "nstance-object-storage-store-test")
	defer func() { _ = os.RemoveAll(tempDir) }()

	mockStorage, err := storage.NewMockStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to create mock storage: %v", err)
	}

	ctx := context.Background()

	t.Run("WithoutEncryptionKey", func(t *testing.T) {
		store := NewObjectStorageStore(mockStorage, "secret/", nil)
		testData := []byte("unencrypted secret")

		err := store.Set(ctx, "test", testData)
		if err != nil {
			t.Fatalf("Failed to set secret: %v", err)
		}

		retrieved, err := store.Get(ctx, "test")
		if err != nil {
			t.Fatalf("Failed to get secret: %v", err)
		}

		if string(retrieved) != string(testData) {
			t.Errorf("Data doesn't match. Expected: %s, Got: %s", string(testData), string(retrieved))
		}
	})

	t.Run("WithEncryptionKey", func(t *testing.T) {
		key := []byte("test-key-32-characters-long!!!!!")
		store := NewObjectStorageStore(mockStorage, "encrypted/", [][]byte{key})
		testData := []byte("encrypted secret")

		err := store.Set(ctx, "test", testData)
		if err != nil {
			t.Fatalf("Failed to set encrypted secret: %v", err)
		}

		retrieved, err := store.Get(ctx, "test")
		if err != nil {
			t.Fatalf("Failed to get encrypted secret: %v", err)
		}

		if string(retrieved) != string(testData) {
			t.Errorf("Decrypted data doesn't match. Expected: %s, Got: %s", string(testData), string(retrieved))
		}

		// Verify it's actually encrypted in storage
		rawData, _, err := mockStorage.Get(ctx, "encrypted/test")
		if err != nil {
			t.Fatalf("Failed to get raw data: %v", err)
		}

		if string(rawData) == string(testData) {
			t.Error("Data should be encrypted in storage")
		}
	})
}

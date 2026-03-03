// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"testing"
)

func TestMemoryStore(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Test basic operations
	testData := []byte("test secret data")

	// Test Set
	err := store.Set(ctx, "test-secret", testData)
	if err != nil {
		t.Fatalf("Failed to set secret: %v", err)
	}

	// Test Get
	retrieved, err := store.Get(ctx, "test-secret")
	if err != nil {
		t.Fatalf("Failed to get secret: %v", err)
	}

	if string(retrieved) != string(testData) {
		t.Errorf("Retrieved data doesn't match. Expected: %s, Got: %s", string(testData), string(retrieved))
	}

	// Test Get non-existent
	_, err = store.Get(ctx, "non-existent")
	if err == nil {
		t.Error("Expected error for non-existent secret")
	}

	// Test Delete
	err = store.Delete(ctx, "test-secret")
	if err != nil {
		t.Fatalf("Failed to delete secret: %v", err)
	}

	// Verify deletion
	_, err = store.Get(ctx, "test-secret")
	if err == nil {
		t.Error("Expected error after deletion")
	}
}

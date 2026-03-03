// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"testing"
	"time"
)

func TestCachedStore_NoCache(t *testing.T) {
	// Test that when TTL is 0, it passes through directly
	underlying := NewMemoryStore()
	_ = underlying.Set(context.Background(), "test", []byte("value"))

	cached := NewCachedStore(underlying, 0)

	// Should return the underlying store directly
	if cached != underlying {
		t.Error("Expected cached store to return underlying store when TTL is 0")
	}
}

func TestCachedStore_CacheMiss(t *testing.T) {
	ctx := context.Background()
	underlying := NewMemoryStore()
	_ = underlying.Set(ctx, "test", []byte("value"))

	cached := NewCachedStore(underlying, 5*time.Minute)

	// First access should cache miss and fetch from underlying
	result, err := cached.Get(ctx, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if string(result) != "value" {
		t.Errorf("Expected 'value', got '%s'", string(result))
	}
}

func TestCachedStore_CacheHit(t *testing.T) {
	ctx := context.Background()
	underlying := NewMemoryStore()
	_ = underlying.Set(ctx, "test", []byte("original"))

	cached := NewCachedStore(underlying, 5*time.Minute).(*Cached)

	// First access - cache miss
	result1, err := cached.Get(ctx, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Change underlying value
	_ = underlying.Set(ctx, "test", []byte("changed"))

	// Second access should return cached value
	result2, err := cached.Get(ctx, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if string(result1) != string(result2) {
		t.Error("Expected cached value to be returned on second access")
	}
	if string(result2) != "original" {
		t.Errorf("Expected cached 'original', got '%s'", string(result2))
	}
}

func TestCachedStore_StaleWhileRevalidate(t *testing.T) {
	ctx := context.Background()
	underlying := NewMemoryStore()
	_ = underlying.Set(ctx, "test", []byte("original"))

	// Use very short TTL for testing
	cached := NewCachedStore(underlying, 1*time.Millisecond).(*Cached)

	// First access - cache miss
	_, err := cached.Get(ctx, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Wait for cache to become stale
	time.Sleep(2 * time.Millisecond)

	// Change underlying value
	_ = underlying.Set(ctx, "test", []byte("updated"))

	// Second access should return stale value initially
	result2, err := cached.Get(ctx, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should still get the original value (stale while revalidate)
	if string(result2) != "original" {
		t.Errorf("Expected stale 'original', got '%s'", string(result2))
	}

	// Give background refresh a moment to complete
	time.Sleep(10 * time.Millisecond)

	// Third access should now return the updated value
	result3, err := cached.Get(ctx, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if string(result3) != "updated" {
		t.Errorf("Expected updated 'updated', got '%s'", string(result3))
	}
}

func TestCachedStore_Delete(t *testing.T) {
	ctx := context.Background()
	underlying := NewMemoryStore()
	_ = underlying.Set(ctx, "test", []byte("value"))

	cached := NewCachedStore(underlying, 5*time.Minute).(*Cached)

	// Cache the value
	_, _ = cached.Get(ctx, "test")

	// Delete should remove from both cache and underlying
	err := cached.Delete(ctx, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should be removed from cache
	cached.mu.RLock()
	_, exists := cached.cache["test"]
	cached.mu.RUnlock()

	if exists {
		t.Error("Expected value to be removed from cache")
	}

	// Should be removed from underlying store
	_, err = underlying.Get(ctx, "test")
	if err == nil {
		t.Error("Expected value to be removed from underlying store")
	}
}

func TestCachedStore_Set(t *testing.T) {
	ctx := context.Background()
	underlying := NewMemoryStore()
	cached := NewCachedStore(underlying, 5*time.Minute)

	// Set should delegate to underlying store
	err := cached.Set(ctx, "test", []byte("value"))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should be in underlying store
	result, err := underlying.Get(ctx, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if string(result) != "value" {
		t.Errorf("Expected 'value', got '%s'", string(result))
	}
}

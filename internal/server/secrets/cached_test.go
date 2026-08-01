// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingStore records underlying Get calls.
type countingStore struct {
	Store
	gets atomic.Int32
}

// blockingStore pauses Get calls until released by a test.
type blockingStore struct {
	Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

// Get reads a value and waits for the test to release it.
func (s *blockingStore) Get(ctx context.Context, name string) ([]byte, error) {
	value, err := s.Store.Get(ctx, name)
	s.once.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
		return value, err
	}
}

// Get records and delegates an underlying read.
func (s *countingStore) Get(ctx context.Context, name string) ([]byte, error) {
	s.gets.Add(1)
	time.Sleep(20 * time.Millisecond)
	return s.Store.Get(ctx, name)
}

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

// TestCachedStoreCoalescesConcurrentMisses verifies one underlying read serves concurrent misses.
func TestCachedStoreCoalescesConcurrentMisses(t *testing.T) {
	ctx := context.Background()
	underlying := NewMemoryStore()
	if err := underlying.Set(ctx, "shared", []byte("value")); err != nil {
		t.Fatal(err)
	}
	counting := &countingStore{Store: underlying}
	cached := NewCachedStoreWithLimit(counting, time.Minute, 2)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := cached.Get(ctx, "shared")
			if err != nil || string(value) != "value" {
				t.Errorf("Get = %q, %v", value, err)
			}
		}()
	}
	wg.Wait()
	if counting.gets.Load() != 1 {
		t.Fatalf("underlying gets = %d, want 1", counting.gets.Load())
	}
}

// TestCachedStoreBoundsEntries verifies the configured cache size limit.
func TestCachedStoreBoundsEntries(t *testing.T) {
	ctx := context.Background()
	underlying := NewMemoryStore()
	for _, name := range []string{"one", "two", "three"} {
		if err := underlying.Set(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	cached := NewCachedStoreWithLimit(underlying, time.Minute, 2).(*Cached)
	for _, name := range []string{"one", "two", "three"} {
		if _, err := cached.Get(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	cached.mu.RLock()
	entries := len(cached.cache)
	cached.mu.RUnlock()
	if entries != 2 {
		t.Fatalf("cache entries = %d, want 2", entries)
	}
}

// TestCachedStoreDoesNotShareMutableContent verifies callers receive defensive copies.
func TestCachedStoreDoesNotShareMutableContent(t *testing.T) {
	ctx := context.Background()
	underlying := NewMemoryStore()
	if err := underlying.Set(ctx, "secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
	cached := NewCachedStore(underlying, time.Minute)
	first, err := cached.Get(ctx, "secret")
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'X'
	second, err := cached.Get(ctx, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != "value" {
		t.Fatalf("cached value = %q, want value", second)
	}
}

// TestCachedStoreMissCannotResurrectValueAfterSet verifies stale reads cannot overwrite writes.
func TestCachedStoreMissCannotResurrectValueAfterSet(t *testing.T) {
	ctx := context.Background()
	memory := NewMemoryStore()
	if err := memory.Set(ctx, "secret", []byte("old")); err != nil {
		t.Fatal(err)
	}
	blocking := &blockingStore{Store: memory, started: make(chan struct{}), release: make(chan struct{})}
	cached := NewCachedStore(blocking, time.Minute).(*Cached)
	done := make(chan error, 1)
	go func() {
		_, err := cached.Get(ctx, "secret")
		done <- err
	}()
	<-blocking.started
	if err := cached.Set(ctx, "secret", []byte("new")); err != nil {
		t.Fatal(err)
	}
	close(blocking.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	value, err := cached.Get(ctx, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "new" {
		t.Fatalf("value after concurrent Set = %q, want new", value)
	}
}

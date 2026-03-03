// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// cachedSecret represents a cached secret with metadata
type cachedSecret struct {
	content    []byte
	fetchedAt  time.Time
	refreshing atomic.Bool
}

// Cached wraps a Store with in-memory caching and stale-while-revalidate strategy
type Cached struct {
	underlying Store
	cache      map[string]*cachedSecret
	cacheTTL   time.Duration
	mu         sync.RWMutex
}

// NewCachedStore creates a new cached store wrapper
func NewCachedStore(underlying Store, ttl time.Duration) Store {
	if ttl == 0 {
		return underlying // No caching, direct passthrough
	}
	return &Cached{
		underlying: underlying,
		cache:      make(map[string]*cachedSecret),
		cacheTTL:   ttl,
	}
}

// Get retrieves a secret with caching logic
func (c *Cached) Get(ctx context.Context, name string) ([]byte, error) {
	c.mu.RLock()
	cached, exists := c.cache[name]
	if !exists {
		c.mu.RUnlock()
		return c.fetchAndCache(ctx, name, time.Now())
	}

	// Cache hit - copy data while holding lock to avoid race
	now := time.Now()
	age := now.Sub(cached.fetchedAt)
	content := cached.content
	shouldRefresh := age >= c.cacheTTL && cached.refreshing.CompareAndSwap(false, true)
	c.mu.RUnlock()

	// Trigger background refresh if stale
	if shouldRefresh {
		// Create a context with a timeout for the background refresh
		go func() {
			// Use a reasonable timeout for the refresh (e.g., cache TTL or a fixed duration)
			refreshCtx, cancel := context.WithTimeout(context.Background(), c.cacheTTL)
			defer cancel()
			c.backgroundRefresh(refreshCtx, name)
		}()
	}

	return content, nil
}

// fetchAndCache fetches a secret and caches it
func (c *Cached) fetchAndCache(ctx context.Context, name string, fetchTime time.Time) ([]byte, error) {
	content, err := c.underlying.Get(ctx, name)
	if err != nil {
		return nil, err
	}

	// Cache the result
	c.mu.Lock()
	c.cache[name] = &cachedSecret{
		content:   content,
		fetchedAt: fetchTime,
	}
	c.mu.Unlock()

	return content, nil
}

// backgroundRefresh refreshes a stale cache entry in the background
func (c *Cached) backgroundRefresh(ctx context.Context, name string) {
	defer func() {
		// Reset refreshing flag
		c.mu.RLock()
		if cached, exists := c.cache[name]; exists {
			cached.refreshing.Store(false)
		}
		c.mu.RUnlock()
	}()

	content, err := c.underlying.Get(ctx, name)
	if err != nil {
		slog.Error("failed to refresh cached secret", "name", name, "error", err)
		return
	}

	// Update cache with fresh content
	c.mu.Lock()
	if cached, exists := c.cache[name]; exists {
		cached.content = content
		cached.fetchedAt = time.Now()
	}
	c.mu.Unlock()
}

// Set delegates to underlying store (no caching needed for writes)
func (c *Cached) Set(ctx context.Context, name string, data []byte) error {
	return c.underlying.Set(ctx, name, data)
}

// Delete delegates to underlying store and removes from cache
func (c *Cached) Delete(ctx context.Context, name string) error {
	err := c.underlying.Delete(ctx, name)
	if err != nil {
		return err
	}

	// Remove from cache
	c.mu.Lock()
	delete(c.cache, name)
	c.mu.Unlock()

	return nil
}

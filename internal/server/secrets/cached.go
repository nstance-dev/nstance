// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const defaultMaxCacheEntries = 256

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
	maxEntries int
	generation uint64
	misses     singleflight.Group
	mu         sync.RWMutex
}

// NewCachedStore creates a new cached store wrapper
func NewCachedStore(underlying Store, ttl time.Duration) Store {
	return NewCachedStoreWithLimit(underlying, ttl, defaultMaxCacheEntries)
}

// NewCachedStoreWithLimit creates a cache with a bounded number of entries.
func NewCachedStoreWithLimit(underlying Store, ttl time.Duration, maxEntries int) Store {
	if ttl == 0 {
		return underlying // No caching, direct passthrough
	}
	if maxEntries <= 0 {
		maxEntries = defaultMaxCacheEntries
	}
	return &Cached{
		underlying: underlying,
		cache:      make(map[string]*cachedSecret),
		cacheTTL:   ttl,
		maxEntries: maxEntries,
	}
}

// Get retrieves a secret with caching logic
func (c *Cached) Get(ctx context.Context, name string) ([]byte, error) {
	c.mu.RLock()
	cached, exists := c.cache[name]
	if !exists {
		generation := c.generation
		c.mu.RUnlock()
		result := c.misses.DoChan(name, func() (any, error) {
			fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.cacheTTL)
			defer cancel()
			return c.fetchAndCache(fetchCtx, name, time.Now(), generation)
		})
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case value := <-result:
			if value.Err != nil {
				return nil, value.Err
			}
			return cloneBytes(value.Val.([]byte)), nil
		}
	}

	// Cache hit - copy data while holding lock to avoid race
	now := time.Now()
	age := now.Sub(cached.fetchedAt)
	content := cloneBytes(cached.content)
	shouldRefresh := age >= c.cacheTTL && cached.refreshing.CompareAndSwap(false, true)
	generation := c.generation
	c.mu.RUnlock()

	// Trigger background refresh if stale
	if shouldRefresh {
		go func() {
			refreshCtx, cancel := context.WithTimeout(context.Background(), c.cacheTTL)
			defer cancel()
			c.backgroundRefresh(refreshCtx, name, cached, generation)
		}()
	}

	return content, nil
}

// fetchAndCache fetches a secret and caches it
func (c *Cached) fetchAndCache(ctx context.Context, name string, fetchTime time.Time, generation uint64) ([]byte, error) {
	content, err := c.underlying.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	content = cloneBytes(content)

	// Cache the result
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return content, nil
	}
	if _, exists := c.cache[name]; !exists && len(c.cache) >= c.maxEntries {
		var oldestName string
		var oldestTime time.Time
		for cachedName, cached := range c.cache {
			if oldestName == "" || cached.fetchedAt.Before(oldestTime) {
				oldestName = cachedName
				oldestTime = cached.fetchedAt
			}
		}
		delete(c.cache, oldestName)
	}
	c.cache[name] = &cachedSecret{
		content:   content,
		fetchedAt: fetchTime,
	}

	return content, nil
}

// backgroundRefresh refreshes a stale cache entry in the background
func (c *Cached) backgroundRefresh(ctx context.Context, name string, original *cachedSecret, generation uint64) {
	defer func() {
		c.mu.RLock()
		if c.cache[name] == original {
			original.refreshing.Store(false)
		}
		c.mu.RUnlock()
	}()

	content, err := c.underlying.Get(ctx, name)
	if err != nil {
		slog.Error("failed to refresh cached secret", "name", name, "error", err)
		return
	}

	c.mu.Lock()
	if c.generation == generation && c.cache[name] == original {
		original.content = cloneBytes(content)
		original.fetchedAt = time.Now()
	}
	c.mu.Unlock()
}

// Set delegates to the underlying store and invalidates any stale cached value.
func (c *Cached) Set(ctx context.Context, name string, data []byte) error {
	if err := c.underlying.Set(ctx, name, data); err != nil {
		return err
	}
	c.mu.Lock()
	c.generation++
	delete(c.cache, name)
	c.mu.Unlock()
	c.misses.Forget(name)
	return nil
}

// Delete delegates to underlying store and removes from cache
func (c *Cached) Delete(ctx context.Context, name string) error {
	err := c.underlying.Delete(ctx, name)
	if err != nil {
		return err
	}

	// Remove from cache
	c.mu.Lock()
	c.generation++
	delete(c.cache, name)
	c.mu.Unlock()
	c.misses.Forget(name)

	return nil
}

// cloneBytes prevents callers from sharing mutable cache storage.
func cloneBytes(data []byte) []byte {
	return append([]byte(nil), data...)
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package images

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nstance-dev/nstance/internal/server/infra"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/localdb"
)

// Service manages periodic image resolution and caching
type Service struct {
	resolver Resolver
	db       *localdb.DB
	configs  map[string]config.ImageConfig
	interval time.Duration
	logger   *slog.Logger

	mu      sync.RWMutex
	images  map[string]string // Resolved image IDs
	stopCh  chan struct{}
	stopped bool
}

// ServiceOptions contains options for creating an image service
type ServiceOptions struct {
	ProviderConfig infra.ProviderConfig
	DB             *localdb.DB
	Configs        map[string]config.ImageConfig
	Interval       time.Duration
	Logger         *slog.Logger
}

// NewService creates a new image resolution service
func NewService(opts ServiceOptions) (*Service, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Interval == 0 {
		opts.Interval = 6 * time.Hour // Default 6 hours
	}

	// Create resolver for the provider
	resolver, err := NewResolver(opts.ProviderConfig.Kind, opts.ProviderConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create image resolver: %w", err)
	}

	return &Service{
		resolver: resolver,
		db:       opts.DB,
		configs:  opts.Configs,
		interval: opts.Interval,
		logger:   opts.Logger,
		images:   make(map[string]string),
		stopCh:   make(chan struct{}),
	}, nil
}

// Start begins the periodic image resolution process
// Should be called when the server becomes shard leader
// Uses SWR (stale-while-revalidate) strategy: returns early if all images are cached,
// otherwise blocks on fresh lookup
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped {
		s.logger.Warn("Cannot start image service - already stopped")
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// Load cached images
	cached, err := s.db.GetImages()
	if err != nil {
		s.logger.Warn("Failed to load cached images", "error", err)
		cached = make(map[string]string)
	}

	// Check if we have ALL configured images in cache
	allCached := true
	for name := range s.configs {
		if _, exists := cached[name]; !exists {
			allCached = false
			s.logger.Debug("Image not in cache", "name", name)
			break
		}
	}

	if allCached && len(cached) > 0 {
		// All images cached - use immediately, refresh in background
		s.mu.Lock()
		s.images = cached
		s.mu.Unlock()
		go s.refreshLoop()
		s.logger.Info("Using cached images (SWR)", "count", len(cached), "interval", s.interval)
		return nil
	}

	// Cache incomplete or empty - block on fresh lookup
	s.logger.Info("Cache incomplete, performing fresh image resolution")
	if err := s.refresh(ctx); err != nil {
		s.logger.Error("Initial image resolution failed", "error", err)
		// Continue anyway - we may have fallbacks
	}

	// Start periodic refresh goroutine
	go s.refreshLoop()

	s.logger.Info("Image resolution service started", "interval", s.interval)
	return nil
}

// Stop stops the periodic refresh
// Should be called when losing shard leadership
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return
	}

	close(s.stopCh)
	s.stopped = true
	s.logger.Info("Image resolution service stopped")
}

// Get returns the resolved image ID for a given image name
// Returns empty string if not resolved
func (s *Service) Get(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.images[name]
}

// GetAll returns all resolved images
func (s *Service) GetAll() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string]string)
	for k, v := range s.images {
		result[k] = v
	}
	return result
}

// refreshLoop periodically refreshes image resolutions
func (s *Service) refreshLoop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := s.refresh(ctx); err != nil {
				s.logger.Error("Periodic image resolution failed", "error", err)
			}
			cancel()
		case <-s.stopCh:
			return
		}
	}
}

// refresh performs a single image resolution cycle
func (s *Service) refresh(ctx context.Context) error {
	s.logger.Info("Image resolution started")

	// If no images configured, nothing to do
	if len(s.configs) == 0 {
		s.logger.Debug("No images configured for resolution")
		return nil
	}

	// Try to load from cache first
	cached, err := s.db.GetImages()
	if err != nil {
		s.logger.Warn("Failed to load image cache from database", "error", err)
		cached = make(map[string]string)
	}

	// Perform fresh lookup
	resolved, err := s.resolver.Resolve(ctx, s.configs)
	if err != nil {
		s.logger.Error("Image resolution failed", "error", err)

		// Use cached values if lookup fails
		if len(cached) > 0 {
			s.logger.Info("Using cached image resolutions", "count", len(cached))
			s.mu.Lock()
			s.images = cached
			s.mu.Unlock()
			return nil
		}

		// No cache available, try fallbacks
		s.applyFallbacks()
		return err
	}

	// Log resolved images
	for name, imageID := range resolved {
		s.logger.Info("Image resolved", "name", name, "image_id", imageID)
	}

	// Update in-memory map
	s.mu.Lock()
	s.images = resolved
	s.mu.Unlock()

	// Save to database cache
	if err := s.db.UpsertImages(resolved, time.Now().UTC()); err != nil {
		s.logger.Warn("Failed to save image cache to database", "error", err)
		// Don't return error - resolution was successful
	}

	s.logger.Info("Image resolution completed", "count", len(resolved))
	return nil
}

// applyFallbacks applies configured fallback image IDs when resolution fails
func (s *Service) applyFallbacks() {
	fallbacks := make(map[string]string)

	for name, cfg := range s.configs {
		if cfg.Fallback != nil && *cfg.Fallback != "" {
			fallbacks[name] = *cfg.Fallback
			s.logger.Info("Using fallback image", "name", name, "image_id", *cfg.Fallback)
		} else {
			s.logger.Warn("No fallback configured for image", "name", name)
		}
	}

	if len(fallbacks) > 0 {
		s.mu.Lock()
		s.images = fallbacks
		s.mu.Unlock()
	}
}

// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
	"github.com/tidwall/jsonc"
)

const (
	defaultMaxRetries = 2
	configKey         = "config.jsonc"
	groupsKey         = "groups.jsonc"
	groupsCacheKey    = "groups.json"
)

// Loader handles loading and caching of configuration from storage.
//
// The loader uses a two-tier caching strategy:
//  1. In-memory cache: fastest access for runtime operations
//  2. Disk cache: persists across process restarts to minimize S3 reads
//
// The disk cache is critical for cost control - if the server enters a crash loop
// or restarts frequently, it prevents excessive S3 GET requests which could become
// expensive at scale. On startup, we first check the disk cache before hitting S3.
//
// The loader expects to receive shard-scoped storage - all paths are relative to
// the shard scope (e.g., "config.jsonc" not "shard/{shard}/config.jsonc").
type Loader struct {
	storage       storage.Storage // Main storage (e.g. S3), scoped to shard
	cacheStorage  storage.Storage // Cache storage (e.g. local filesystem), scoped to shard
	localDB       *localdb.DB     // Local SQLite database for caching groups with hashes
	logger        *slog.Logger
	maxRetries    int
	advertiseHost string // CLI override for advertise health/election host
	configMu      sync.RWMutex
	config        *Config
	configMeta    *ConfigMetadata // Metadata of the currently loaded config

	groupsMu sync.RWMutex
	groups   map[string]map[string]GroupConfig // tenant -> group key -> config
}

// LoaderOptions contains options for creating a new Loader
type LoaderOptions struct {
	Storage       storage.Storage // Main storage (e.g. S3), scoped to shard
	CacheStorage  storage.Storage // Cache storage (e.g. local filesystem), scoped to shard
	LocalDB       *localdb.DB     // Local SQLite database for caching groups with hashes
	Logger        *slog.Logger
	MaxRetries    *int   // Maximum retry attempts (nil = use default)
	AdvertiseHost string // CLI override for advertise health/election host (empty = use auto-detection)
}

// NewLoader creates a new configuration loader.
// Storage and CacheStorage should be scoped to the shard - the loader uses
// relative paths like "config.jsonc" and "groups.jsonc".
func NewLoader(opts LoaderOptions) (*Loader, error) {
	if opts.Storage == nil {
		return nil, fmt.Errorf("storage is required")
	}
	if opts.CacheStorage == nil {
		return nil, fmt.Errorf("cache storage is required")
	}
	if opts.LocalDB == nil {
		return nil, fmt.Errorf("local database is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	return &Loader{
		storage:       opts.Storage,
		cacheStorage:  opts.CacheStorage,
		localDB:       opts.LocalDB,
		logger:        opts.Logger,
		maxRetries:    maxRetries,
		advertiseHost: opts.AdvertiseHost,
	}, nil
}

// LoadConfigAndGroups loads the configuration and dynamic groups from storage.
// If forceRefresh is false, it will use local cache if available (faster startup).
// If forceRefresh is true, it always downloads from S3 (for explicit refresh).
// After loading, it syncs all groups to SQLite for runtime operations.
func (l *Loader) LoadConfigAndGroups(ctx context.Context, forceRefresh bool) (*Config, error) {
	l.configMu.Lock()
	defer l.configMu.Unlock()

	var configData []byte
	var etag string

	// Try disk cache first (unless forcing refresh)
	if !forceRefresh {
		l.logger.Info("Loading configuration")
		if exists, _ := l.cacheStorage.Exists(ctx, configKey); exists {
			if data, tag, err := l.cacheStorage.Get(ctx, configKey); err == nil {
				if _, err := l.parseAndValidate(data); err == nil {
					configData, etag = data, tag
					l.logger.Info("Loaded configuration from disk cache")
				} else {
					l.logger.Warn("Cached configuration is invalid, will fetch from S3", "error", err)
					_ = l.cacheStorage.Delete(ctx, configKey)
				}
			}
		}
	} else {
		l.logger.Info("Refreshing configuration from storage")
	}

	// Download from S3 if cache miss
	if configData == nil {
		var err error
		configData, etag, err = l.downloadWithRetry(ctx, configKey)
		if err != nil {
			return nil, fmt.Errorf("failed to download configuration: %w", err)
		}
		if err := l.cacheStorage.Put(ctx, configKey, configData); err != nil {
			l.logger.Warn("Failed to save configuration to cache", "error", err)
		}
	}

	// Parse and validate configuration
	config, err := l.parseAndValidate(configData)
	if err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	// Update in-memory state
	l.config = config
	l.configMeta = &ConfigMetadata{
		ETag: etag,
		Size: int64(len(configData)),
	}

	l.logger.Info("Loaded configuration", "size", len(configData))

	// Load dynamic groups (uses its own lock, so release ours)
	l.configMu.Unlock()
	_, err = l.LoadDynamicGroups(ctx)
	l.configMu.Lock()
	if err != nil {
		return nil, fmt.Errorf("failed to load dynamic groups: %w", err)
	}

	// Sync all groups to SQLite
	l.configMu.Unlock()
	err = l.SyncGroupsToCache(ctx)
	l.configMu.Lock()
	if err != nil {
		return nil, fmt.Errorf("failed to sync groups to cache: %w", err)
	}

	// Apply advertise host override or auto-detect empty/wildcard hosts
	if err := autoDetectAdvertiseHosts(config, l.logger, l.advertiseHost); err != nil {
		return nil, err
	}

	return config, nil
}

// GetCurrent returns the currently loaded configuration
func (l *Loader) GetCurrent() *Config {
	l.configMu.RLock()
	defer l.configMu.RUnlock()
	return l.config
}

// GetMetadata returns the metadata of the currently loaded configuration
func (l *Loader) GetMetadata() *ConfigMetadata {
	l.configMu.RLock()
	defer l.configMu.RUnlock()
	return l.configMeta
}

// SetConfig directly sets the configuration (for testing only)
func (l *Loader) SetConfig(config *Config) {
	l.configMu.Lock()
	defer l.configMu.Unlock()
	l.config = config
}

// parseAndValidate parses and validates configuration data
func (l *Loader) parseAndValidate(data []byte) (*Config, error) {
	// Convert JSONC to standard JSON
	jsonData := jsonc.ToJSON(data)

	var config Config
	if err := json.Unmarshal(jsonData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse configuration JSONC: %w", err)
	}

	// Set defaults
	config.SetDefaults()

	// Validate
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

// downloadWithRetry downloads from storage with exponential backoff retry
func (l *Loader) downloadWithRetry(ctx context.Context, key string) ([]byte, string, error) {
	var lastErr error

	for attempt := 0; attempt <= l.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			if backoff > 32*time.Second {
				backoff = 32 * time.Second
			}
			l.logger.Info("Retrying configuration download", "attempt", attempt, "backoff", backoff)

			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(backoff):
			}
		}

		data, etag, err := l.storage.Get(ctx, key)
		if err != nil {
			lastErr = err
			l.logger.Warn("Configuration download failed", "attempt", attempt, "error", err)
			continue
		}

		return data, etag, nil
	}

	return nil, "", fmt.Errorf("storage download failed after %d attempts: %w", l.maxRetries+1, lastErr)
}

// CleanCache removes all cached files (config and groups)
func (l *Loader) CleanCache(ctx context.Context) error {
	if err := l.cacheStorage.Delete(ctx, configKey); err != nil {
		l.logger.Warn("Failed to delete configuration cache", "error", err)
	}

	if err := l.cacheStorage.Delete(ctx, groupsCacheKey); err != nil {
		l.logger.Warn("Failed to delete groups cache", "error", err)
	}

	return nil
}

// RefreshGroups forces a refresh of the dynamic groups from storage
func (l *Loader) RefreshGroups(ctx context.Context) error {
	// Clear disk cache to force reload from storage
	if err := l.cacheStorage.Delete(ctx, groupsCacheKey); err != nil {
		l.logger.Warn("Failed to clear groups disk cache", "error", err)
	}

	// Invalidate in-memory cache
	l.groupsMu.Lock()
	l.groups = nil
	l.groupsMu.Unlock()

	// Now call LoadDynamicGroups which will reload from storage
	_, err := l.LoadDynamicGroups(ctx)
	return err
}

// GetDynamicGroup returns a dynamic group from the in-memory cache for a specific tenant
// Returns empty config and false if not found, not loaded, or tenant is empty
// This method never triggers disk or S3 reads
func (l *Loader) GetDynamicGroup(tenant, key string) (GroupConfig, bool) {
	if tenant == "" {
		return GroupConfig{}, false
	}

	l.groupsMu.RLock()
	defer l.groupsMu.RUnlock()

	if l.groups == nil {
		return GroupConfig{}, false
	}

	tenantGroups, exists := l.groups[tenant]
	if !exists {
		return GroupConfig{}, false
	}

	group, exists := tenantGroups[key]
	return group, exists
}

// LoadDynamicGroups loads dynamic groups from S3 groups.jsonc.
// Returns the cached map for read-only access. Do not mutate the returned map;
// use SetDynamicGroup/DeleteDynamicGroup for mutations.
func (l *Loader) LoadDynamicGroups(ctx context.Context) (map[string]map[string]GroupConfig, error) {
	l.groupsMu.RLock()
	if l.groups != nil {
		groups := l.groups
		l.groupsMu.RUnlock()
		return groups, nil
	}
	l.groupsMu.RUnlock()

	l.groupsMu.Lock()
	defer l.groupsMu.Unlock()

	// Double-check after acquiring write lock
	if l.groups != nil {
		return l.groups, nil
	}

	// Step 1: Try to load from local disk cache
	groups, ok := l.loadGroupsFromCache(ctx, groupsCacheKey)
	if ok {
		l.logger.Info("Loaded dynamic groups from local cache")
		l.groups = groups
		return l.groups, nil
	}

	// Step 2: Load from S3
	data, _, err := l.storage.Get(ctx, groupsKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// File doesn't exist yet - cache and return empty map
			l.groups = make(map[string]map[string]GroupConfig)
			return l.groups, nil
		}
		return nil, fmt.Errorf("failed to load dynamic groups: %w", err)
	}

	// Step 3: Parse groups (nested by tenant)
	groups, err = l.parseGroups(data)
	if err != nil {
		return nil, err
	}

	// Step 4: Save to disk cache
	if err := l.saveGroupsToCache(ctx, groupsCacheKey, data); err != nil {
		l.logger.Warn("Failed to save groups to cache", "error", err)
	}

	// Step 5: Update in-memory cache
	l.groups = groups
	return groups, nil
}

// SetDynamicGroup atomically updates a single dynamic group and persists to S3.
// This is the only safe way to mutate dynamic groups.
// Uses ETag from disk cache to prevent concurrent modification.
// Updates both in-memory and disk cache on successful write.
func (l *Loader) SetDynamicGroup(ctx context.Context, tenant, key string, group GroupConfig) error {
	l.groupsMu.Lock()
	defer l.groupsMu.Unlock()

	// Ensure groups are loaded
	if l.groups == nil {
		l.groups = make(map[string]map[string]GroupConfig)
	}

	// Ensure tenant map exists
	if l.groups[tenant] == nil {
		l.groups[tenant] = make(map[string]GroupConfig)
	}

	// Update the group
	l.groups[tenant][key] = group

	// Persist
	return l.saveDynamicGroupsLocked(ctx)
}

// DeleteDynamicGroup atomically removes a dynamic group and persists to S3.
func (l *Loader) DeleteDynamicGroup(ctx context.Context, tenant, key string) error {
	l.groupsMu.Lock()
	defer l.groupsMu.Unlock()

	if l.groups == nil || l.groups[tenant] == nil {
		return nil // Nothing to delete
	}

	if _, exists := l.groups[tenant][key]; !exists {
		return nil // Nothing to delete
	}

	delete(l.groups[tenant], key)

	// Clean up empty tenant map
	if len(l.groups[tenant]) == 0 {
		delete(l.groups, tenant)
	}

	return l.saveDynamicGroupsLocked(ctx)
}

// saveDynamicGroupsLocked persists dynamic groups to S3. Caller must hold groupsMu.
func (l *Loader) saveDynamicGroupsLocked(ctx context.Context) error {
	// Compute ETag from disk cache (empty if never loaded/doesn't exist)
	expectedETag := ""
	cachedData, _, err := l.cacheStorage.Get(ctx, groupsCacheKey)
	if err == nil {
		expectedETag = fmt.Sprintf("%x", md5.Sum(cachedData))
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(l.groups, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal dynamic groups: %w", err)
	}

	// Write to S3 with ETag check (optimistic locking)
	if err := l.storage.PutIfMatch(ctx, groupsKey, data, expectedETag); err != nil {
		if errors.Is(err, storage.ErrPrecondition) {
			return fmt.Errorf("groups file was modified by another process")
		}
		return fmt.Errorf("failed to write dynamic groups: %w", err)
	}

	// Update disk cache
	if err := l.saveGroupsToCache(ctx, groupsCacheKey, data); err != nil {
		l.logger.Warn("Failed to update groups cache", "error", err)
	}

	return nil
}

// loadGroupsFromCache attempts to load groups from local disk cache
// Groups are nested by tenant: map[tenant]map[groupKey]GroupConfig
func (l *Loader) loadGroupsFromCache(ctx context.Context, cacheKey string) (map[string]map[string]GroupConfig, bool) {
	data, _, err := l.cacheStorage.Get(ctx, cacheKey)
	if err != nil {
		return nil, false
	}

	// Parse groups (nested by tenant)
	var groups map[string]map[string]GroupConfig
	if err := json.Unmarshal(data, &groups); err != nil {
		l.logger.Warn("Cached groups are invalid", "error", err)
		return nil, false
	}

	return groups, true
}

// parseGroups parses groups data from JSONC format
// Groups are nested by tenant: map[tenant]map[groupKey]GroupConfig
func (l *Loader) parseGroups(data []byte) (map[string]map[string]GroupConfig, error) {
	// Convert JSONC to standard JSON
	jsonData := jsonc.ToJSON(data)

	// Parse into nested map: tenant -> group key -> config
	var groups map[string]map[string]GroupConfig
	if err := json.Unmarshal(jsonData, &groups); err != nil {
		return nil, fmt.Errorf("failed to parse dynamic groups JSONC: %w", err)
	}

	if groups == nil {
		groups = make(map[string]map[string]GroupConfig)
	}

	return groups, nil
}

// saveGroupsToCache saves groups to local disk cache
func (l *Loader) saveGroupsToCache(ctx context.Context, cacheKey string, groupsData []byte) error {
	if err := l.cacheStorage.Put(ctx, cacheKey, groupsData); err != nil {
		return fmt.Errorf("failed to write cache entry: %w", err)
	}
	return nil
}

// isWildcardHost returns true if the host is empty or a "wildcard" address.
func isWildcardHost(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

// autoDetectAdvertiseHosts fills in empty or wildcard hosts in advertise addrs.
// If advertiseHost is set (from --advertise-host CLI flag), it overrides the host
// portion of health_addr and election_addr only. Other addrs fall through to
// auto-detection if needed.
// Uses the bind health addr's host to determine IPv4 vs IPv6 preference.
func autoDetectAdvertiseHosts(config *Config, logger *slog.Logger, advertiseHost string) error {
	// Per-instance addrs that --advertise-host overrides
	perInstanceFields := []struct {
		name string
		addr *string
	}{
		{"health_addr", &config.Shard.Advertise.HealthAddr},
		{"election_addr", &config.Shard.Advertise.ElectionAddr},
	}

	// Apply --advertise-host override for per-instance addrs
	if advertiseHost != "" {
		for _, f := range perInstanceFields {
			host, port, err := net.SplitHostPort(*f.addr)
			if err != nil {
				return fmt.Errorf("failed to parse advertise %s: %w", f.name, err)
			}
			if !isWildcardHost(host) {
				return fmt.Errorf("--advertise-host cannot be used when advertise.%s already specifies a host (%s); use port-only format (e.g. \":%s\")", f.name, host, port)
			}
			*f.addr = net.JoinHostPort(advertiseHost, port)
			logger.Info("Applied --advertise-host override", "field", f.name, "addr", *f.addr)
		}
	}

	// All advertise addrs that may need auto-detection
	allFields := []struct {
		name string
		addr *string
	}{
		{"health_addr", &config.Shard.Advertise.HealthAddr},
		{"election_addr", &config.Shard.Advertise.ElectionAddr},
		{"registration_addr", &config.Shard.Advertise.RegistrationAddr},
		{"operator_addr", &config.Shard.Advertise.OperatorAddr},
		{"agent_addr", &config.Shard.Advertise.AgentAddr},
	}

	// Check if any remaining field needs auto-detection
	needsDetection := false
	for _, f := range allFields {
		host, _, err := net.SplitHostPort(*f.addr)
		if err != nil {
			continue
		}
		if isWildcardHost(host) {
			needsDetection = true
			break
		}
	}

	if !needsDetection {
		return nil
	}

	// Determine IP version preference from bind health addr
	bindHost, _, err := net.SplitHostPort(config.Shard.Bind.HealthAddr)
	if err != nil {
		return fmt.Errorf("failed to parse bind health addr for auto-detection: %w", err)
	}

	detectedIP, err := detectPrimaryIP(bindHost)
	if err != nil {
		return fmt.Errorf("failed to auto-detect advertise host: %w", err)
	}

	// Fill in all addrs that still need it
	for _, f := range allFields {
		host, port, err := net.SplitHostPort(*f.addr)
		if err != nil {
			continue
		}
		if isWildcardHost(host) {
			*f.addr = net.JoinHostPort(detectedIP, port)
			logger.Info("Auto-detected advertise host", "field", f.name, "addr", *f.addr)
		}
	}

	return nil
}

// SyncGroupsToCache syncs all effective groups to SQLite with computed hashes.
// This should be called after config or groups are loaded/reloaded.
// Groups cannot exist in SQLite without hashes - they are computed and stored atomically.
func (l *Loader) SyncGroupsToCache(ctx context.Context) error {
	l.configMu.RLock()
	config := l.config
	l.configMu.RUnlock()

	if config == nil {
		return fmt.Errorf("configuration not loaded")
	}

	l.groupsMu.RLock()
	dynamicGroups := l.groups
	l.groupsMu.RUnlock()

	if dynamicGroups == nil {
		dynamicGroups = make(map[string]map[string]GroupConfig)
	}

	// Collect all tenant keys from both static and dynamic
	tenants := make(map[string]struct{})
	for tenant := range config.Groups {
		tenants[tenant] = struct{}{}
	}
	for tenant := range dynamicGroups {
		tenants[tenant] = struct{}{}
	}

	var failures []string
	for tenant := range tenants {
		mergedGroups := mergeGroups(config.Groups[tenant], dynamicGroups[tenant])
		for groupKey, groupConfig := range mergedGroups {
			// Get template name from group config
			template := groupConfig.Template
			if template == "" {
				l.logger.Warn("skipping group sync", "tenant", tenant, "group", groupKey, "error", "no template")
				failures = append(failures, fmt.Sprintf("%s in tenant %s: no template", groupKey, tenant))
				continue
			}

			// Get merged config for hash computation
			mergedConfig, err := config.GetMergedConfig(template, groupConfig)
			if err != nil {
				l.logger.Error("Failed to get merged config", "tenant", tenant, "group", groupKey, "error", err)
				failures = append(failures, fmt.Sprintf("%s in tenant %s: %v", groupKey, tenant, err))
				continue
			}

			// Compure hashes
			runtimeHash := HashRuntimeConfig(*mergedConfig)
			infraHash := HashInfraConfig(*mergedConfig)

			// Store in SQLite
			if err := l.localDB.UpsertGroup(tenant, groupKey, runtimeHash, infraHash); err != nil {
				l.logger.Error("Failed to upsert group", "tenant", tenant, "group", groupKey, "error", err)
				failures = append(failures, fmt.Sprintf("%s/%s: %v", tenant, groupKey, err))
				continue
			}

			l.logger.Debug("Synced group to cache",
				"tenant", tenant,
				"group", groupKey,
				"runtime_hash", runtimeHash,
				"infra_hash", infraHash)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("failed to sync %d groups to cache: %v", len(failures), failures)
	}

	return nil
}

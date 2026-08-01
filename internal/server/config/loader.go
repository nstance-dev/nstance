// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"sync"
	"time"

	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
	"github.com/tailscale/hujson"
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
	if err := autoDetectAdvertiseHosts(config, l.logger, l.advertiseHost); err != nil {
		return nil, err
	}

	// Load groups and compute their hashes before replacing the current config and
	// groups, so readers continue to see the previous consistent state on error.
	l.groupsMu.Lock()
	defer l.groupsMu.Unlock()
	var dynamicGroups map[string]map[string]GroupConfig
	if forceRefresh {
		dynamicGroups, _, err = l.loadDynamicGroupsFromStorage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load dynamic groups: %w", err)
		}
	} else {
		dynamicGroups = l.groups
	}
	if dynamicGroups == nil {
		if cached, ok := l.loadGroupsFromCache(ctx, groupsCacheKey); ok {
			dynamicGroups = cached
		} else {
			dynamicGroups, _, err = l.loadDynamicGroupsFromStorage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to load dynamic groups: %w", err)
			}
		}
	}
	candidateHashes, err := l.computeGroupHashes(config, dynamicGroups)
	if err != nil {
		return nil, fmt.Errorf("failed to compute group hashes: %w", err)
	}
	if err := l.localDB.ReplaceGroupHashes(candidateHashes); err != nil {
		return nil, fmt.Errorf("failed to sync groups to cache: %w", err)
	}

	// Replace the in-memory config and groups only after their hashes are stored.
	l.groups = dynamicGroups
	l.config = config
	l.configMeta = &ConfigMetadata{ETag: etag, Size: int64(len(configData))}
	l.logger.Info("Loaded configuration", "size", len(configData))
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

// GetCachedGroup returns stored hashes while preventing a concurrent config or
// dynamic-group update.
func (l *Loader) GetCachedGroup(tenant, group string) (*localdb.Group, error) {
	l.configMu.RLock()
	defer l.configMu.RUnlock()
	return l.localDB.GetGroup(tenant, group)
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
	jsonData, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse configuration JSONC: %w", err)
	}

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
	groups, _, err := l.loadDynamicGroupsFromStorage(ctx)
	if err != nil {
		return nil, err
	}
	l.groups = groups
	return groups, nil
}

// loadDynamicGroupsFromStorage loads dynamic groups without consulting or mutating
// the in-memory cache. The caller must hold groupsMu before installing the result.
func (l *Loader) loadDynamicGroupsFromStorage(ctx context.Context) (map[string]map[string]GroupConfig, string, error) {
	// Load current groups and the backend validator used for conditional writes.
	data, etag, err := l.storage.Get(ctx, groupsKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return make(map[string]map[string]GroupConfig), "", nil
		}
		return nil, "", fmt.Errorf("failed to load dynamic groups: %w", err)
	}

	// Parse groups nested by tenant.
	groups, err := l.parseGroups(data)
	if err != nil {
		return nil, "", err
	}

	// Save the authoritative data to the local disk cache.
	if err := l.saveGroupsToCache(ctx, groupsCacheKey, data); err != nil {
		l.logger.Warn("Failed to save groups to cache", "error", err)
	}
	return groups, etag, nil
}

// SetDynamicGroup atomically updates a single dynamic group and persists to S3.
// This is the only safe way to mutate dynamic groups.
// Uses the authoritative storage ETag to prevent concurrent modification.
// Updates both in-memory and disk cache on successful write.
func (l *Loader) SetDynamicGroup(ctx context.Context, tenant, key string, group GroupConfig) error {
	return l.UpdateDynamicGroup(ctx, tenant, key, func(GroupConfig, bool) (GroupConfig, error) {
		return group, nil
	})
}

// UpdateDynamicGroup atomically reloads, merges, and persists one dynamic group.
// The update callback runs while configMu and groupsMu are held and must not call Loader methods.
func (l *Loader) UpdateDynamicGroup(
	ctx context.Context,
	tenant, key string,
	update func(existing GroupConfig, exists bool) (GroupConfig, error),
) error {
	l.configMu.Lock()
	defer l.configMu.Unlock()
	l.groupsMu.Lock()
	defer l.groupsMu.Unlock()

	groups, etag, err := l.loadDynamicGroupsFromStorage(ctx)
	if err != nil {
		return err
	}
	existing, exists := groups[tenant][key]
	group, err := update(cloneGroupConfig(existing), exists)
	if err != nil {
		return err
	}

	candidate := cloneDynamicGroups(groups)
	if candidate[tenant] == nil {
		candidate[tenant] = make(map[string]GroupConfig)
	}
	candidate[tenant][key] = group
	return l.saveDynamicGroupsLocked(ctx, candidate, etag, l.config)
}

// DeleteDynamicGroup atomically removes a dynamic group and persists to S3.
func (l *Loader) DeleteDynamicGroup(ctx context.Context, tenant, key string) error {
	l.configMu.Lock()
	defer l.configMu.Unlock()
	l.groupsMu.Lock()
	defer l.groupsMu.Unlock()

	groups, etag, err := l.loadDynamicGroupsFromStorage(ctx)
	if err != nil {
		return err
	}
	if groups[tenant] == nil {
		return l.publishLoadedDynamicGroupsLocked(ctx, groups, l.config)
	}

	if _, exists := groups[tenant][key]; !exists {
		return l.publishLoadedDynamicGroupsLocked(ctx, groups, l.config)
	}

	candidate := cloneDynamicGroups(groups)
	delete(candidate[tenant], key)

	// Clean up empty tenant map
	if len(candidate[tenant]) == 0 {
		delete(candidate, tenant)
	}

	return l.saveDynamicGroupsLocked(ctx, candidate, etag, l.config)
}

// saveDynamicGroupsLocked persists and publishes dynamic groups. The caller must
// hold configMu and groupsMu so readers cannot observe mismatched config and groups.
func (l *Loader) saveDynamicGroupsLocked(ctx context.Context, groups map[string]map[string]GroupConfig, etag string, config *Config) error {
	// Marshal to JSON
	data, err := json.MarshalIndent(groups, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal dynamic groups: %w", err)
	}
	hashes, err := l.computeGroupHashes(config, groups)
	if err != nil {
		return fmt.Errorf("failed to compute group hashes: %w", err)
	}

	// Write to S3 with ETag check (optimistic locking)
	if err := l.storage.PutIfMatch(ctx, groupsKey, data, etag); err != nil {
		if errors.Is(err, storage.ErrPrecondition) {
			return fmt.Errorf("groups file was modified by another process")
		}
		return fmt.Errorf("failed to write dynamic groups: %w", err)
	}
	if err := l.publishDynamicGroupsLocallyLocked(ctx, groups, data, hashes); err != nil {
		// Do not let a restart load the stale disk cache after the shared write committed.
		if cacheErr := l.cacheStorage.Delete(ctx, groupsCacheKey); cacheErr != nil {
			l.logger.Warn("Failed to invalidate stale groups cache", "error", cacheErr)
		}
		return fmt.Errorf("dynamic groups saved but failed to update local state: %w", err)
	}
	return nil
}

// publishLoadedDynamicGroupsLocked computes hashes for loaded groups and updates
// the local database, disk cache, and in-memory group map.
func (l *Loader) publishLoadedDynamicGroupsLocked(ctx context.Context, groups map[string]map[string]GroupConfig, config *Config) error {
	data, err := json.MarshalIndent(groups, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal dynamic groups: %w", err)
	}
	hashes, err := l.computeGroupHashes(config, groups)
	if err != nil {
		return fmt.Errorf("failed to compute group hashes: %w", err)
	}
	return l.publishDynamicGroupsLocallyLocked(ctx, groups, data, hashes)
}

// publishDynamicGroupsLocallyLocked replaces the stored group hashes, updates the
// best-effort disk cache, and then makes the groups available to local readers.
func (l *Loader) publishDynamicGroupsLocallyLocked(ctx context.Context, groups map[string]map[string]GroupConfig, data []byte, hashes []localdb.GroupHashes) error {
	if err := l.localDB.ReplaceGroupHashes(hashes); err != nil {
		return fmt.Errorf("failed to publish group hashes: %w", err)
	}
	if err := l.saveGroupsToCache(ctx, groupsCacheKey, data); err != nil {
		l.logger.Warn("Failed to update groups cache", "error", err)
	}
	l.groups = groups
	return nil
}

// cloneDynamicGroups copies the tenant and group maps for a candidate update.
func cloneDynamicGroups(groups map[string]map[string]GroupConfig) map[string]map[string]GroupConfig {
	clone := make(map[string]map[string]GroupConfig, len(groups))
	for tenant, tenantGroups := range groups {
		clone[tenant] = maps.Clone(tenantGroups)
	}
	return clone
}

// cloneGroupConfig copies mutable maps in a group configuration.
func cloneGroupConfig(group GroupConfig) GroupConfig {
	group.Vars = maps.Clone(group.Vars)
	group.Args = maps.Clone(group.Args)
	return group
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
	jsonData, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse dynamic groups JSONC: %w", err)
	}

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

	hashes, err := l.computeGroupHashes(config, dynamicGroups)
	if err != nil {
		return err
	}
	return l.localDB.ReplaceGroupHashes(hashes)
}

// computeGroupHashes computes effective runtime and infrastructure hashes for every group.
func (l *Loader) computeGroupHashes(config *Config, dynamicGroups map[string]map[string]GroupConfig) ([]localdb.GroupHashes, error) {
	if config == nil {
		return nil, nil
	}
	// Collect all tenant keys from both static and dynamic.
	tenants := make(map[string]struct{})
	for tenant := range config.Groups {
		tenants[tenant] = struct{}{}
	}
	for tenant := range dynamicGroups {
		tenants[tenant] = struct{}{}
	}

	var hashes []localdb.GroupHashes
	for tenant := range tenants {
		mergedGroups := mergeGroups(config.Groups[tenant], dynamicGroups[tenant])
		for groupKey, groupConfig := range mergedGroups {
			// Get template name from group config
			template := groupConfig.Template
			if template == "" {
				return nil, fmt.Errorf("%s in tenant %s: no template", groupKey, tenant)
			}

			// Get merged config for hash computation
			mergedConfig, err := config.GetMergedConfig(template, groupConfig)
			if err != nil {
				return nil, fmt.Errorf("%s in tenant %s: %w", groupKey, tenant, err)
			}

			// Compure hashes
			runtimeHash := HashRuntimeConfig(*mergedConfig)
			infraHash := HashInfraConfig(*mergedConfig)

			hashes = append(hashes, localdb.GroupHashes{Tenant: tenant, GroupKey: groupKey, RuntimeHash: runtimeHash, InfraHash: infraHash})
		}
	}
	return hashes, nil
}

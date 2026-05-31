// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

func newTestDB(t *testing.T) *localdb.DB {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	db, err := localdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	})
	return db
}

func TestLoader(t *testing.T) {
	// Create test configuration
	testConfig := &Config{
		Cluster: ClusterConfig{
			ID: "example-cluster",
			Secrets: SecretsConfig{
				Provider: "memory",
			},
		},
		Shard: ShardConfig{
			ID: "test-shard",
			Infra: InfraConfig{
				Provider: "aws",
				Region:   "us-west-2",
				Zone:     "us-west-2a",
			},
			LeaderNetwork: &LeaderNetworkConfig{IP: "172.16.0.100", InterfaceID: "eni-test123"},
			Bind: BindConfig{
				HealthAddr:       "0.0.0.0:8990",
				ElectionAddr:     "0.0.0.0:8991",
				RegistrationAddr: "0.0.0.0:8992",
				OperatorAddr:     "0.0.0.0:8993",
				AgentAddr:        "0.0.0.0:8994",
			},
			Advertise: AdvertiseConfig{
				HealthAddr:       "172.16.0.1:8990",
				ElectionAddr:     "172.16.0.1:8991",
				RegistrationAddr: "172.16.0.1:8992",
				OperatorAddr:     "172.16.0.1:8993",
				AgentAddr:        "172.16.0.1:8994",
			},
			SubnetPools: map[string][]string{
				"default": {"subnet-123"},
			},
		},
		Templates: map[string]TemplateConfig{
			"test": {
				Kind:       "tst",
				Arch:       "amd64",
				SubnetPool: "default",
			},
		},
	}

	// Set defaults and marshal
	testConfig.SetDefaults()
	configData, err := json.MarshalIndent(testConfig, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal test config: %v", err)
	}

	ctx := context.Background()

	t.Run("LoadFromStorage", func(t *testing.T) {
		// Create mock storages (these are already shard-scoped in real usage)
		mainStorage := storage.NewMock()
		cacheStorage := storage.NewMock()

		// Put config in main storage using relative path
		configKey := "config.jsonc"
		err := mainStorage.Put(ctx, configKey, configData)
		if err != nil {
			t.Fatalf("Failed to put config in main storage: %v", err)
		}

		// Create loader (receives shard-scoped storage)
		loader, err := NewLoader(LoaderOptions{
			LocalDB:      newTestDB(t),
			Storage:      mainStorage,
			CacheStorage: cacheStorage,
			Logger:       slog.Default(),
		})
		if err != nil {
			t.Fatalf("Failed to create loader: %v", err)
		}

		// Load configuration
		config, err := loader.LoadConfigAndGroups(ctx, false)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}

		// Verify config
		if config.Shard.Infra.Provider != "aws" {
			t.Errorf("Expected provider 'aws', got '%s'", config.Shard.Infra.Provider)
		}
		if config.Shard.ID != "test-shard" {
			t.Errorf("Expected shard 'test-shard', got '%s'", config.Shard.ID)
		}

		// Verify cache was populated
		cacheKey := "config.jsonc"
		exists, err := cacheStorage.Exists(ctx, cacheKey)
		if err != nil {
			t.Fatalf("Failed to check cache existence: %v", err)
		}
		if !exists {
			t.Error("Configuration should be cached")
		}
	})

	t.Run("LoadFromCache", func(t *testing.T) {
		// Create mock storages
		mainStorage := storage.NewMock()
		cacheStorage := storage.NewMock()

		// Put config in cache only (relative path)
		cacheKey := "config.jsonc"
		err := cacheStorage.Put(ctx, cacheKey, configData)
		if err != nil {
			t.Fatalf("Failed to put config in cache: %v", err)
		}

		// Put metadata in cache
		metadata := &ConfigMetadata{
			ETag:         "test-etag",
			LastModified: time.Now().UTC(),
			Size:         int64(len(configData)),
		}
		metaData, err := json.Marshal(metadata)
		if err != nil {
			t.Fatalf("Failed to marshal metadata: %v", err)
		}
		metaKey := "config.json.meta"
		err = cacheStorage.Put(ctx, metaKey, metaData)
		if err != nil {
			t.Fatalf("Failed to put metadata in cache: %v", err)
		}

		// Create loader
		loader, err := NewLoader(LoaderOptions{
			LocalDB:      newTestDB(t),
			Storage:      mainStorage,
			CacheStorage: cacheStorage,
			Logger:       slog.Default(),
		})
		if err != nil {
			t.Fatalf("Failed to create loader: %v", err)
		}

		// Load configuration (should load from cache, not main storage)
		config, err := loader.LoadConfigAndGroups(ctx, false)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}

		// Verify config
		if config.Shard.Infra.Provider != "aws" {
			t.Errorf("Expected provider 'aws', got '%s'", config.Shard.Infra.Provider)
		}

		// Verify main storage was not accessed
		configKey := "config.jsonc"
		exists, err := mainStorage.Exists(ctx, configKey)
		if err != nil {
			t.Fatalf("Failed to check main storage: %v", err)
		}
		if exists {
			t.Error("Main storage should not have been accessed when loading from cache")
		}
	})

	t.Run("LoadStorageFailure", func(t *testing.T) {
		// Create mock storages - don't put anything in main storage
		mainStorage := storage.NewMock()
		cacheStorage := storage.NewMock()

		// Create loader with no retries for faster tests
		maxRetries := 0
		loader, err := NewLoader(LoaderOptions{
			LocalDB:      newTestDB(t),
			Storage:      mainStorage,
			CacheStorage: cacheStorage,
			Logger:       slog.Default(),
			MaxRetries:   &maxRetries,
		})
		if err != nil {
			t.Fatalf("Failed to create loader: %v", err)
		}

		// Load configuration should fail
		_, err = loader.LoadConfigAndGroups(ctx, false)
		if err == nil {
			t.Error("Expected error when config not found in storage")
		}
	})

	t.Run("InvalidCachedConfig", func(t *testing.T) {
		// Create mock storages
		mainStorage := storage.NewMock()
		cacheStorage := storage.NewMock()

		// Put invalid config in cache (relative path)
		cacheKey := "config.jsonc"
		invalidData := []byte("invalid json")
		err := cacheStorage.Put(ctx, cacheKey, invalidData)
		if err != nil {
			t.Fatalf("Failed to put invalid config in cache: %v", err)
		}

		// Put valid config in main storage (relative path)
		configKey := "config.jsonc"
		err = mainStorage.Put(ctx, configKey, configData)
		if err != nil {
			t.Fatalf("Failed to put config in main storage: %v", err)
		}

		// Create loader
		loader, err := NewLoader(LoaderOptions{
			LocalDB:      newTestDB(t),
			Storage:      mainStorage,
			CacheStorage: cacheStorage,
			Logger:       slog.Default(),
		})
		if err != nil {
			t.Fatalf("Failed to create loader: %v", err)
		}

		// Load configuration (should fall back to main storage)
		config, err := loader.LoadConfigAndGroups(ctx, false)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}

		// Verify config loaded from main storage
		if config.Shard.Infra.Provider != "aws" {
			t.Errorf("Expected provider 'aws', got '%s'", config.Shard.Infra.Provider)
		}

		// Verify cache was updated with valid config
		cachedData, _, err := cacheStorage.Get(ctx, cacheKey)
		if err != nil {
			t.Fatalf("Failed to get cached data: %v", err)
		}
		if string(cachedData) == string(invalidData) {
			t.Error("Cache should have been updated with valid config")
		}
	})

	t.Run("Refresh", func(t *testing.T) {
		// Create mock storages
		mainStorage := storage.NewMock()
		cacheStorage := storage.NewMock()

		// Put initial config in main storage (relative path)
		configKey := "config.jsonc"
		err := mainStorage.Put(ctx, configKey, configData)
		if err != nil {
			t.Fatalf("Failed to put config in main storage: %v", err)
		}

		// Create loader and load initial config
		loader, err := NewLoader(LoaderOptions{
			LocalDB:      newTestDB(t),
			Storage:      mainStorage,
			CacheStorage: cacheStorage,
			Logger:       slog.Default(),
		})
		if err != nil {
			t.Fatalf("Failed to create loader: %v", err)
		}

		_, err = loader.LoadConfigAndGroups(ctx, false)
		if err != nil {
			t.Fatalf("Failed to load initial config: %v", err)
		}

		// Update config in main storage
		updatedConfig := *testConfig
		updatedConfig.Shard.Infra.Region = "us-east-1"
		updatedConfig.SetDefaults()
		updatedConfigData, err := json.MarshalIndent(&updatedConfig, "", "  ")
		if err != nil {
			t.Fatalf("Failed to marshal updated config: %v", err)
		}

		err = mainStorage.Put(ctx, configKey, updatedConfigData)
		if err != nil {
			t.Fatalf("Failed to put updated config: %v", err)
		}

		// Refresh configuration (forceRefresh=true)
		refreshedConfig, err := loader.LoadConfigAndGroups(ctx, true)
		if err != nil {
			t.Fatalf("Failed to refresh config: %v", err)
		}

		// Verify updated config
		if refreshedConfig.Shard.Infra.Region != "us-east-1" {
			t.Errorf("Expected region 'us-east-1', got '%s'", refreshedConfig.Shard.Infra.Region)
		}
	})

	t.Run("CleanCache", func(t *testing.T) {
		// Create mock storages
		mainStorage := storage.NewMock()
		cacheStorage := storage.NewMock()

		// Put config in cache (relative path)
		cacheKey := "config.jsonc"
		err := cacheStorage.Put(ctx, cacheKey, configData)
		if err != nil {
			t.Fatalf("Failed to put config in cache: %v", err)
		}

		// Create loader
		loader, err := NewLoader(LoaderOptions{
			LocalDB:      newTestDB(t),
			Storage:      mainStorage,
			CacheStorage: cacheStorage,
			Logger:       slog.Default(),
		})
		if err != nil {
			t.Fatalf("Failed to create loader: %v", err)
		}

		// Clean cache
		err = loader.CleanCache(ctx)
		if err != nil {
			t.Fatalf("Failed to clean cache: %v", err)
		}

		// Verify cache was cleaned
		exists, err := cacheStorage.Exists(ctx, cacheKey)
		if err != nil {
			t.Fatalf("Failed to check cache existence: %v", err)
		}
		if exists {
			t.Error("Cache should be cleaned")
		}
	})
}

func TestLoaderValidation(t *testing.T) {

	t.Run("RequiredStorage", func(t *testing.T) {
		_, err := NewLoader(LoaderOptions{
			CacheStorage: storage.NewMock(),
		})
		if err == nil {
			t.Error("Expected error when storage is not provided")
		}
	})

	t.Run("RequiredCacheStorage", func(t *testing.T) {
		_, err := NewLoader(LoaderOptions{
			LocalDB: newTestDB(t),
			Storage: storage.NewMock(),
		})
		if err == nil {
			t.Error("Expected error when cache storage is not provided")
		}
	})

	t.Run("RequiredLocalDB", func(t *testing.T) {
		_, err := NewLoader(LoaderOptions{
			Storage:      storage.NewMock(),
			CacheStorage: storage.NewMock(),
		})
		if err == nil {
			t.Error("Expected error when local database is not provided")
		}
	})
}

func TestLoaderRetryLogic(t *testing.T) {
	ctx := context.Background()

	// Create test configuration
	testConfig := &Config{
		Cluster: ClusterConfig{
			ID: "example-cluster",
			Secrets: SecretsConfig{
				Provider: "memory",
			},
		},
		Shard: ShardConfig{
			ID: "test-shard",
			Infra: InfraConfig{
				Provider: "aws",
				Region:   "us-west-2",
				Zone:     "us-west-2a",
			},
			LeaderNetwork: &LeaderNetworkConfig{IP: "172.16.0.100", InterfaceID: "eni-test123"},
			Bind: BindConfig{
				HealthAddr:       "0.0.0.0:8990",
				ElectionAddr:     "0.0.0.0:8991",
				RegistrationAddr: "0.0.0.0:8992",
				OperatorAddr:     "0.0.0.0:8993",
				AgentAddr:        "0.0.0.0:8994",
			},
			Advertise: AdvertiseConfig{
				HealthAddr:       "172.16.0.1:8990",
				ElectionAddr:     "172.16.0.1:8991",
				RegistrationAddr: "172.16.0.1:8992",
				OperatorAddr:     "172.16.0.1:8993",
				AgentAddr:        "172.16.0.1:8994",
			},
		},
		Templates: map[string]TemplateConfig{
			"test": {
				Kind: "tst",
				Arch: "amd64",
			},
		},
	}
	testConfig.SetDefaults()

	t.Run("LoadMissingConfig", func(t *testing.T) {
		// Create mock storage
		mainStorage := storage.NewMock()
		cacheStorage := storage.NewMock()

		// Create loader with only 1 retry for faster test
		maxRetries := 1
		loader, err := NewLoader(LoaderOptions{
			LocalDB:      newTestDB(t),
			Storage:      mainStorage,
			CacheStorage: cacheStorage,
			Logger:       slog.Default(),
			MaxRetries:   &maxRetries,
		})
		if err != nil {
			t.Fatalf("Failed to create loader: %v", err)
		}

		// Load configuration without any config in storage (should fail)
		_, err = loader.LoadConfigAndGroups(ctx, false)
		if err == nil {
			t.Fatal("Expected error when loading missing config, got nil")
		}
	})
}

func TestAutoDetectAdvertiseHosts(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	t.Run("OverrideWithWildcardAddrs", func(t *testing.T) {
		config := &Config{
			Shard: ShardConfig{
				Bind: BindConfig{
					HealthAddr: "0.0.0.0:8990",
				},
				Advertise: AdvertiseConfig{
					HealthAddr:       ":8990",
					ElectionAddr:     "0.0.0.0:8991",
					RegistrationAddr: "10.0.0.1:8992",
					OperatorAddr:     "10.0.0.1:8993",
					AgentAddr:        "10.0.0.1:8994",
				},
			},
		}

		err := autoDetectAdvertiseHosts(config, logger, "192.168.1.50")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if config.Shard.Advertise.HealthAddr != "192.168.1.50:8990" {
			t.Errorf("expected health_addr 192.168.1.50:8990, got %s", config.Shard.Advertise.HealthAddr)
		}
		if config.Shard.Advertise.ElectionAddr != "192.168.1.50:8991" {
			t.Errorf("expected election_addr 192.168.1.50:8991, got %s", config.Shard.Advertise.ElectionAddr)
		}
		// Leader-service addrs should be unchanged
		if config.Shard.Advertise.RegistrationAddr != "10.0.0.1:8992" {
			t.Errorf("registration_addr should be unchanged, got %s", config.Shard.Advertise.RegistrationAddr)
		}
	})

	t.Run("OverrideWithIPv6Wildcard", func(t *testing.T) {
		config := &Config{
			Shard: ShardConfig{
				Bind: BindConfig{
					HealthAddr: "0.0.0.0:8990",
				},
				Advertise: AdvertiseConfig{
					HealthAddr:       ":8990",
					ElectionAddr:     "[::]:8991",
					RegistrationAddr: "10.0.0.1:8992",
					OperatorAddr:     "10.0.0.1:8993",
					AgentAddr:        "10.0.0.1:8994",
				},
			},
		}

		err := autoDetectAdvertiseHosts(config, logger, "10.0.0.5")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if config.Shard.Advertise.HealthAddr != "10.0.0.5:8990" {
			t.Errorf("expected health_addr 10.0.0.5:8990, got %s", config.Shard.Advertise.HealthAddr)
		}
		if config.Shard.Advertise.ElectionAddr != "10.0.0.5:8991" {
			t.Errorf("expected election_addr 10.0.0.5:8991, got %s", config.Shard.Advertise.ElectionAddr)
		}
	})

	t.Run("ErrorWhenExplicitHostSet", func(t *testing.T) {
		config := &Config{
			Shard: ShardConfig{
				Bind: BindConfig{
					HealthAddr: "0.0.0.0:8990",
				},
				Advertise: AdvertiseConfig{
					HealthAddr:       "172.16.0.1:8990",
					ElectionAddr:     ":8991",
					RegistrationAddr: "10.0.0.1:8992",
					OperatorAddr:     "10.0.0.1:8993",
					AgentAddr:        "10.0.0.1:8994",
				},
			},
		}

		err := autoDetectAdvertiseHosts(config, logger, "192.168.1.50")
		if err == nil {
			t.Fatal("expected error when health_addr has explicit host")
		}
		if !strings.Contains(err.Error(), "--advertise-host cannot be used") {
			t.Errorf("unexpected error message: %v", err)
		}
		if !strings.Contains(err.Error(), "health_addr") {
			t.Errorf("error should mention health_addr: %v", err)
		}
	})

	t.Run("ErrorWhenElectionHasExplicitHost", func(t *testing.T) {
		config := &Config{
			Shard: ShardConfig{
				Bind: BindConfig{
					HealthAddr: "0.0.0.0:8990",
				},
				Advertise: AdvertiseConfig{
					HealthAddr:       ":8990",
					ElectionAddr:     "172.16.0.1:8991",
					RegistrationAddr: "10.0.0.1:8992",
					OperatorAddr:     "10.0.0.1:8993",
					AgentAddr:        "10.0.0.1:8994",
				},
			},
		}

		err := autoDetectAdvertiseHosts(config, logger, "192.168.1.50")
		if err == nil {
			t.Fatal("expected error when election_addr has explicit host")
		}
		if !strings.Contains(err.Error(), "election_addr") {
			t.Errorf("error should mention election_addr: %v", err)
		}
	})

	t.Run("NoOverrideFallsThrough", func(t *testing.T) {
		config := &Config{
			Shard: ShardConfig{
				Bind: BindConfig{
					HealthAddr: "0.0.0.0:8990",
				},
				Advertise: AdvertiseConfig{
					HealthAddr:       "172.16.0.1:8990",
					ElectionAddr:     "172.16.0.1:8991",
					RegistrationAddr: "172.16.0.1:8992",
					OperatorAddr:     "172.16.0.1:8993",
					AgentAddr:        "172.16.0.1:8994",
				},
			},
		}

		// No override (empty string) - should pass through unchanged since all have explicit hosts
		err := autoDetectAdvertiseHosts(config, logger, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if config.Shard.Advertise.HealthAddr != "172.16.0.1:8990" {
			t.Errorf("health_addr should be unchanged, got %s", config.Shard.Advertise.HealthAddr)
		}
	})
}

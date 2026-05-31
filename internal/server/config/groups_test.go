// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

func newTestConfig() *Config {
	return &Config{
		Cluster: ClusterConfig{
			ID:      "cls123",
			Secrets: SecretsConfig{Provider: "memory"},
		},
		Shard: ShardConfig{
			ID:            "test-shard",
			Infra:         InfraConfig{Provider: "mock", Region: "us-west-2", Zone: "us-west-2a"},
			LeaderNetwork: &LeaderNetworkConfig{IP: "172.16.0.100", InterfaceID: "eni-test"},
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
				"default1":  {"subnet-default1"},
				"default2":  {"subnet-default2"},
				"primary":   {"subnet-123"},
				"secondary": {"subnet-456"},
			},
		},
		Templates: map[string]TemplateConfig{
			"knc": {Kind: "knc", Arch: "arm64", SubnetPool: "default1"},
			"knd": {Kind: "knd", Arch: "arm64", SubnetPool: "default1"},
		},
	}
}

func newTestLoader(t *testing.T, cfg *Config) *Loader {
	mainStorage := storage.NewMock()
	cacheStorage := storage.NewMock()

	// Create temporary database for tests
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

	loader, err := NewLoader(LoaderOptions{
		Storage:      mainStorage,
		CacheStorage: cacheStorage,
		LocalDB:      db,
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create loader: %v", err)
	}

	loader.config = cfg
	return loader
}

func TestMergeGroups(t *testing.T) {
	tests := []struct {
		name     string
		static   map[string]GroupConfig
		dynamic  map[string]GroupConfig
		expected map[string]GroupConfig
	}{
		{
			name: "static only",
			static: map[string]GroupConfig{
				"main": {
					Template:     "knc",
					Size:         IntPtr(1),
					InstanceType: "t4g.medium",
					SubnetPool:   "subnet-123",
					Vars:         map[string]string{"env": "prod"},
				},
			},
			dynamic: map[string]GroupConfig{},
			expected: map[string]GroupConfig{
				"main": {
					Template:     "knc",
					Size:         IntPtr(1),
					InstanceType: "t4g.medium",
					SubnetPool:   "subnet-123",
					Vars:         map[string]string{"env": "prod"},
				},
			},
		},
		{
			name:   "dynamic only",
			static: map[string]GroupConfig{},
			dynamic: map[string]GroupConfig{
				"apps": {
					Template:     "knd",
					Size:         IntPtr(3),
					InstanceType: "t4g.xlarge",
					SubnetPool:   "subnet-456",
					Vars:         map[string]string{"role": "apps"},
				},
			},
			expected: map[string]GroupConfig{
				"apps": {
					Template:     "knd",
					Size:         IntPtr(3),
					InstanceType: "t4g.xlarge",
					SubnetPool:   "subnet-456",
					Vars:         map[string]string{"role": "apps"},
				},
			},
		},
		{
			name: "dynamic overrides static",
			static: map[string]GroupConfig{
				"main": {
					Template:     "knc",
					Size:         IntPtr(1),
					InstanceType: "t4g.medium",
					SubnetPool:   "subnet-123",
					Vars:         map[string]string{"env": "prod"},
				},
			},
			dynamic: map[string]GroupConfig{
				"main": {
					Size:         IntPtr(5),
					InstanceType: "t4g.xlarge",
					Vars:         map[string]string{"extra": "label"},
				},
			},
			expected: map[string]GroupConfig{
				"main": {
					Template:     "knc",
					Size:         IntPtr(5),
					InstanceType: "t4g.xlarge",
					SubnetPool:   "subnet-123",
					Vars: map[string]string{
						"env":   "prod",
						"extra": "label",
					},
				},
			},
		},
		{
			name: "dynamic scales to zero",
			static: map[string]GroupConfig{
				"main": {
					Template:     "knc",
					Size:         IntPtr(5),
					InstanceType: "t4g.medium",
					SubnetPool:   "subnet-123",
				},
			},
			dynamic: map[string]GroupConfig{
				"main": {
					Size: IntPtr(0),
				},
			},
			expected: map[string]GroupConfig{
				"main": {
					Template:     "knc",
					Size:         IntPtr(0),
					InstanceType: "t4g.medium",
					SubnetPool:   "subnet-123",
				},
			},
		},
		{
			name: "dynamic omits size - fallback to static",
			static: map[string]GroupConfig{
				"main": {
					Template:     "knc",
					Size:         IntPtr(5),
					InstanceType: "t4g.medium",
					SubnetPool:   "subnet-123",
				},
			},
			dynamic: map[string]GroupConfig{
				"main": {
					InstanceType: "t4g.xlarge",
				},
			},
			expected: map[string]GroupConfig{
				"main": {
					Template:     "knc",
					Size:         IntPtr(5),
					InstanceType: "t4g.xlarge",
					SubnetPool:   "subnet-123",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeGroups(tt.static, tt.dynamic)

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d groups, got %d", len(tt.expected), len(result))
			}

			for key, expectedGroup := range tt.expected {
				resultGroup, exists := result[key]
				if !exists {
					t.Fatalf("expected group %s not found in result", key)
				}

				if resultGroup.Template != expectedGroup.Template {
					t.Errorf("group %s: expected template %s, got %s", key, expectedGroup.Template, resultGroup.Template)
				}
				if resultGroup.GetSize() != expectedGroup.GetSize() {
					t.Errorf("group %s: expected size %d, got %d", key, expectedGroup.GetSize(), resultGroup.GetSize())
				}
				if resultGroup.InstanceType != expectedGroup.InstanceType {
					t.Errorf("group %s: expected instanceType %s, got %s", key, expectedGroup.InstanceType, resultGroup.InstanceType)
				}
			}
		})
	}
}

func TestUpsertGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("upsert new dynamic group", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Groups = map[string]map[string]GroupConfig{"default": {}}
		loader := newTestLoader(t, cfg)

		newGroup := GroupConfig{
			Template:     "knc",
			Size:         IntPtr(3),
			InstanceType: "t4g.xlarge",
			SubnetPool:   "primary",
			Vars:         map[string]string{"role": "apps"},
		}

		err := UpsertGroup(ctx, loader, "default", "apps", newGroup)
		if err != nil {
			t.Fatalf("UpsertGroup failed: %v", err)
		}

		// Verify group was saved
		group, err := GetGroup(ctx, loader, "default", "apps")
		if err != nil {
			t.Fatalf("GetGroup failed: %v", err)
		}
		if group.GetSize() != 3 {
			t.Errorf("expected size 3, got %d", group.GetSize())
		}
		if group.Template != "knc" {
			t.Errorf("expected template knc, got %s", group.Template)
		}
	})

	t.Run("upsert override for static group", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Groups = map[string]map[string]GroupConfig{
			"default": {
				"main": {
					Template:     "knc",
					Size:         IntPtr(1),
					InstanceType: "t4g.medium",
					SubnetPool:   "primary",
				},
			},
		}
		loader := newTestLoader(t, cfg)

		override := GroupConfig{
			Size:         IntPtr(5),
			InstanceType: "t4g.xlarge",
			Vars:         map[string]string{"extra": "label"},
		}

		err := UpsertGroup(ctx, loader, "default", "main", override)
		if err != nil {
			t.Fatalf("UpsertGroup failed: %v", err)
		}

		// Verify merged group
		group, err := GetGroup(ctx, loader, "default", "main")
		if err != nil {
			t.Fatalf("GetGroup failed: %v", err)
		}
		if group.GetSize() != 5 {
			t.Errorf("expected size 5, got %d", group.GetSize())
		}
		if group.Template != "knc" {
			t.Errorf("expected template knc (from static), got %s", group.Template)
		}
		if group.SubnetPool != "primary" {
			t.Errorf("expected subnet pool primary from static config, got %s", group.SubnetPool)
		}
	})

	t.Run("cannot override template for static group", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Groups = map[string]map[string]GroupConfig{
			"default": {
				"main": {
					Template:     "knc",
					Size:         IntPtr(1),
					InstanceType: "t4g.medium",
					SubnetPool:   "subnet-123",
				},
			},
		}
		loader := newTestLoader(t, cfg)

		override := GroupConfig{
			Template: "knd",
			Size:     IntPtr(5),
		}

		err := UpsertGroup(ctx, loader, "default", "main", override)
		if err == nil {
			t.Fatal("expected error when overriding template, got nil")
		}
	})

	t.Run("cannot override subnet pool for static group", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Groups = map[string]map[string]GroupConfig{
			"default": {
				"main": {
					Template:     "knc",
					Size:         IntPtr(1),
					InstanceType: "t4g.medium",
					SubnetPool:   "subnet-123",
				},
			},
		}
		loader := newTestLoader(t, cfg)

		override := GroupConfig{
			Size:       IntPtr(5),
			SubnetPool: "subnet-456",
		}

		err := UpsertGroup(ctx, loader, "default", "main", override)
		if err == nil {
			t.Fatal("expected error when overriding subnet, got nil")
		}
	})

	t.Run("upsert new dynamic group inherits template subnet", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Groups = map[string]map[string]GroupConfig{"default": {}}
		loader := newTestLoader(t, cfg)

		newGroup := GroupConfig{
			Template:     "knc",
			Size:         IntPtr(3),
			InstanceType: "t4g.xlarge",
			// No subnet specified - should inherit from template
			Vars: map[string]string{"role": "apps"},
		}

		err := UpsertGroup(ctx, loader, "default", "apps", newGroup)
		if err != nil {
			t.Fatalf("UpsertGroup failed: %v", err)
		}

		// Verify group was saved with template's subnet (logical key)
		group, err := GetGroup(ctx, loader, "default", "apps")
		if err != nil {
			t.Fatalf("GetGroup failed: %v", err)
		}
		if group.SubnetPool != "default1" {
			t.Errorf("expected subnet pool default1 from template, got %s", group.SubnetPool)
		}
	})

	t.Run("upsert new dynamic group with invalid subnet pool", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Groups = map[string]map[string]GroupConfig{"default": {}}
		loader := newTestLoader(t, cfg)

		newGroup := GroupConfig{
			Template:   "knc",
			Size:       IntPtr(3),
			SubnetPool: "nonexistent-key",
		}

		err := UpsertGroup(ctx, loader, "default", "apps", newGroup)
		if err == nil {
			t.Fatal("expected error for invalid subnet pool, got nil")
		}
		if !strings.Contains(err.Error(), "unknown subnet pool") {
			t.Errorf("expected error about unknown subnet pool, got: %v", err)
		}
	})

	t.Run("upsert new dynamic group with subnet pool not in dynamicSubnets", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Shard.SubnetPools["restricted"] = []string{"subnet-restricted"}
		cfg.Shard.DynamicSubnetPools = []string{"default1"}
		cfg.Groups = map[string]map[string]GroupConfig{"default": {}}
		loader := newTestLoader(t, cfg)

		newGroup := GroupConfig{
			Template:   "knc",
			Size:       IntPtr(3),
			SubnetPool: "restricted",
		}

		err := UpsertGroup(ctx, loader, "default", "apps", newGroup)
		if err == nil {
			t.Fatal("expected error for restricted subnet pool, got nil")
		}
		if !strings.Contains(err.Error(), "not allowed for external requests") {
			t.Errorf("expected error about restricted subnet, got: %v", err)
		}
	})

	t.Run("upsert new dynamic group fails if template has no subnet", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Templates["no-subnet"] = TemplateConfig{Kind: "tst", Arch: "arm64"}
		cfg.Groups = map[string]map[string]GroupConfig{"default": {}}
		loader := newTestLoader(t, cfg)

		newGroup := GroupConfig{
			Template: "no-subnet",
			Size:     IntPtr(3),
		}

		err := UpsertGroup(ctx, loader, "default", "apps", newGroup)
		if err == nil {
			t.Fatal("expected error for template with no subnet, got nil")
		}
		if !strings.Contains(err.Error(), "has no subnet pool configured") {
			t.Errorf("expected error about template subnet pool, got: %v", err)
		}
	})

	t.Run("upsert new dynamic group fails if template not found", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Groups = map[string]map[string]GroupConfig{"default": {}}
		loader := newTestLoader(t, cfg)

		newGroup := GroupConfig{
			Template: "nonexistent",
			Size:     IntPtr(3),
		}

		err := UpsertGroup(ctx, loader, "default", "apps", newGroup)
		if err == nil {
			t.Fatal("expected error for nonexistent template, got nil")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected error about template not found, got: %v", err)
		}
	})
}

func TestDeleteGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("delete dynamic-only group", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Groups = map[string]map[string]GroupConfig{"default": {}}
		loader := newTestLoader(t, cfg)

		// Create a dynamic group first
		newGroup := GroupConfig{
			Template:   "knc",
			Size:       IntPtr(3),
			SubnetPool: "primary",
		}
		err := UpsertGroup(ctx, loader, "default", "apps", newGroup)
		if err != nil {
			t.Fatalf("UpsertGroup failed: %v", err)
		}

		// Delete it
		err = DeleteGroup(ctx, loader, "default", "apps")
		if err != nil {
			t.Fatalf("DeleteGroup failed: %v", err)
		}

		// Verify it's gone
		_, err = GetGroup(ctx, loader, "default", "apps")
		if err == nil {
			t.Fatal("expected error when getting deleted group, got nil")
		}
	})

	t.Run("delete dynamic override for static group", func(t *testing.T) {
		cfg := newTestConfig()
		cfg.Groups = map[string]map[string]GroupConfig{
			"default": {
				"main": {
					Template:     "knc",
					Size:         IntPtr(1),
					InstanceType: "t4g.medium",
					SubnetPool:   "primary",
				},
			},
		}
		loader := newTestLoader(t, cfg)

		// Add dynamic override
		override := GroupConfig{
			Size:         IntPtr(5),
			InstanceType: "t4g.xlarge",
		}
		err := UpsertGroup(ctx, loader, "default", "main", override)
		if err != nil {
			t.Fatalf("UpsertGroup failed: %v", err)
		}

		// Delete dynamic override
		err = DeleteGroup(ctx, loader, "default", "main")
		if err != nil {
			t.Fatalf("DeleteGroup failed: %v", err)
		}

		// Verify static group remains with original values
		group, err := GetGroup(ctx, loader, "default", "main")
		if err != nil {
			t.Fatalf("GetGroup failed: %v", err)
		}
		if group.GetSize() != 1 {
			t.Errorf("expected static size 1 after deleting override, got %d", group.GetSize())
		}
		if group.InstanceType != "t4g.medium" {
			t.Errorf("expected static instanceType t4g.medium after deleting override, got %s", group.InstanceType)
		}
	})
}

func TestGetGroups(t *testing.T) {
	ctx := context.Background()

	cfg := newTestConfig()
	cfg.Groups = map[string]map[string]GroupConfig{
		"default": {
			"main": {
				Template:     "knc",
				Size:         IntPtr(1),
				InstanceType: "t4g.medium",
				SubnetPool:   "primary",
			},
		},
	}
	loader := newTestLoader(t, cfg)

	// Add a dynamic group
	newGroup := GroupConfig{
		Template:   "knd",
		Size:       IntPtr(3),
		SubnetPool: "secondary",
	}
	err := UpsertGroup(ctx, loader, "default", "apps", newGroup)
	if err != nil {
		t.Fatalf("UpsertGroup failed: %v", err)
	}

	// Get all groups
	groups, err := GetGroups(ctx, loader, "default")
	if err != nil {
		t.Fatalf("GetGroups failed: %v", err)
	}

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	if _, exists := groups["main"]; !exists {
		t.Error("expected main group to exist")
	}
	if _, exists := groups["apps"]; !exists {
		t.Error("expected apps group to exist")
	}
}

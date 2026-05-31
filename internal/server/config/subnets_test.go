// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
)

func TestResolveSubnetKey(t *testing.T) {
	cfg := &Config{
		Shard: ShardConfig{
			SubnetPools: map[string][]string{
				"primary": {"subnet-p1", "subnet-p2", "subnet-p3"},
				"workers": {"subnet-w1", "subnet-w2"},
			},
		},
	}

	t.Run("resolve existing key", func(t *testing.T) {
		result, err := cfg.ResolveSubnetKey("primary")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 3 {
			t.Errorf("expected 3 subnet IDs, got %d", len(result))
		}
	})

	t.Run("resolve unknown key", func(t *testing.T) {
		_, err := cfg.ResolveSubnetKey("nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown key")
		}
		if !strings.Contains(err.Error(), "unknown subnet pool") {
			t.Errorf("expected unknown subnet pool error, got: %v", err)
		}
	})

	t.Run("resolve empty key returns nil", func(t *testing.T) {
		result, err := cfg.ResolveSubnetKey("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil result for empty key, got: %v", result)
		}
	})
}

func TestValidateDynamicSubnetKey(t *testing.T) {
	t.Run("empty dynamicSubnets allows all", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"workers":  {"subnet-w1"},
					"internal": {"subnet-i1"},
				},
				DynamicSubnetPools: []string{},
			},
		}

		err := cfg.ValidateDynamicSubnetKey("workers")
		if err != nil {
			t.Fatalf("expected no error when dynamicSubnets is empty, got: %v", err)
		}

		err = cfg.ValidateDynamicSubnetKey("internal")
		if err != nil {
			t.Fatalf("expected no error when dynamicSubnets is empty, got: %v", err)
		}
	})

	t.Run("dynamicSubnets restricts keys", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"workers":  {"subnet-w1"},
					"internal": {"subnet-i1"},
					"database": {"subnet-db1"},
				},
				DynamicSubnetPools: []string{"workers"},
			},
		}

		err := cfg.ValidateDynamicSubnetKey("workers")
		if err != nil {
			t.Fatalf("expected no error for allowed key, got: %v", err)
		}

		err = cfg.ValidateDynamicSubnetKey("database")
		if err == nil {
			t.Fatal("expected error for restricted key")
		}
		if !strings.Contains(err.Error(), "not allowed for external requests") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("unknown key returns error", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"workers": {"subnet-w1"},
				},
				DynamicSubnetPools: []string{"workers"},
			},
		}

		err := cfg.ValidateDynamicSubnetKey("nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown key")
		}
		if !strings.Contains(err.Error(), "unknown subnet pool") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty key returns no error", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"workers": {"subnet-w1"},
				},
				DynamicSubnetPools: []string{"workers"},
			},
		}

		err := cfg.ValidateDynamicSubnetKey("")
		if err != nil {
			t.Fatalf("expected no error for empty key, got: %v", err)
		}
	})
}

func TestValidateSubnetConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"primary":   {"subnet-p1"},
					"secondary": {"subnet-s1", "subnet-s2"},
				},
				DynamicSubnetPools: []string{"primary"},
			},
			Templates: map[string]TemplateConfig{
				"test": {Kind: "tst", SubnetPool: "primary"},
			},
			Groups: map[string]map[string]GroupConfig{
				"default": {
					"main": {Template: "test", SubnetPool: "secondary"},
				},
			},
		}

		err := cfg.ValidateSubnetConfig()
		if err != nil {
			t.Fatalf("expected valid config, got: %v", err)
		}
	})

	t.Run("empty subnet pools map", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{},
			},
		}

		err := cfg.ValidateSubnetConfig()
		if err == nil {
			t.Fatal("expected error for empty subnet pools map")
		}
		if !strings.Contains(err.Error(), "shard.subnet_pools must be configured with at least one subnet pool") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty subnet list for key", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"empty": {},
				},
			},
		}

		err := cfg.ValidateSubnetConfig()
		if err == nil {
			t.Fatal("expected error for empty subnet list")
		}
		if !strings.Contains(err.Error(), "has no associated subnet IDs") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("overlapping subnet IDs", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"primary":   {"subnet-shared", "subnet-p1"},
					"secondary": {"subnet-shared", "subnet-s1"},
				},
			},
		}

		err := cfg.ValidateSubnetConfig()
		if err == nil {
			t.Fatal("expected error for overlapping subnet IDs")
		}
		if !strings.Contains(err.Error(), "appears in multiple subnet pools") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("dynamicSubnetPools references unknown subnet pool", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"primary": {"subnet-p1"},
				},
				DynamicSubnetPools: []string{"nonexistent"},
			},
		}

		err := cfg.ValidateSubnetConfig()
		if err == nil {
			t.Fatal("expected error for unknown dynamicSubnetPools entry")
		}
		if !strings.Contains(err.Error(), "dynamic_subnet_pools references unknown subnet pool") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("template references unknown key", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"primary": {"subnet-p1"},
				},
			},
			Templates: map[string]TemplateConfig{
				"test": {Kind: "tst", SubnetPool: "nonexistent"},
			},
		}

		err := cfg.ValidateSubnetConfig()
		if err == nil {
			t.Fatal("expected error for unknown template subnet pool")
		}
		if !strings.Contains(err.Error(), "template \"test\" references unknown subnet pool") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("group references unknown key", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"primary": {"subnet-p1"},
				},
			},
			Groups: map[string]map[string]GroupConfig{
				"default": {
					"main": {Template: "test", SubnetPool: "nonexistent"},
				},
			},
		}

		err := cfg.ValidateSubnetConfig()
		if err == nil {
			t.Fatal("expected error for unknown group subnet pool")
		}
		if !strings.Contains(err.Error(), "group \"main\" (tenant \"default\") references unknown subnet pool") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty subnet pool in template is valid", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"primary": {"subnet-p1"},
				},
			},
			Templates: map[string]TemplateConfig{
				"test": {Kind: "tst", SubnetPool: ""},
			},
		}

		err := cfg.ValidateSubnetConfig()
		if err != nil {
			t.Fatalf("expected no error for empty subnet pool, got: %v", err)
		}
	})

	t.Run("empty subnet pool in group is valid", func(t *testing.T) {
		cfg := &Config{
			Shard: ShardConfig{
				SubnetPools: map[string][]string{
					"primary": {"subnet-p1"},
				},
			},
			Groups: map[string]map[string]GroupConfig{
				"default": {
					"main": {Template: "test", SubnetPool: ""},
				},
			},
		}

		err := cfg.ValidateSubnetConfig()
		if err != nil {
			t.Fatalf("expected no error for empty subnet pool, got: %v", err)
		}
	})
}

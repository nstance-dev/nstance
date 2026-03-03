// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDurationUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"string 30s", `"30s"`, 30 * time.Second, false},
		{"string 5m", `"5m"`, 5 * time.Minute, false},
		{"string 1h", `"1h"`, time.Hour, false},
		{"string 1h30m", `"1h30m"`, 90 * time.Minute, false},
		{"zero string", `"0s"`, 0, false},
		{"invalid string", `"invalid"`, 0, true},
		{"number rejected", `1000000000`, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := json.Unmarshal([]byte(tt.input), &d)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && d.Duration() != tt.expected {
				t.Errorf("Unmarshal() = %v, want %v", d.Duration(), tt.expected)
			}
		})
	}
}

func TestDurationMarshalJSON(t *testing.T) {
	d := Duration(30 * time.Second)
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `"30s"` {
		t.Errorf("Marshal() = %s, want %s", string(data), `"30s"`)
	}
}

func TestConfigValidation(t *testing.T) {
	t.Run("ValidConfig", func(t *testing.T) {
		config := &Config{
			Cluster: ClusterConfig{
				ID: "example-cluster",
				Secrets: SecretsConfig{
					Provider: "object-storage",
					EncryptionKey: &EncryptionKeyConfig{
						Provider: "env",
						Source:   "TEST_ENCRYPTION_KEY",
					},
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

		config.SetDefaults()
		if config.Shard.GarbageCollection.Interval != Duration(2*time.Minute) {
			t.Errorf("expected garbage collection interval default to be 2m, got %v", config.Shard.GarbageCollection.Interval)
		}
		if config.Shard.GarbageCollection.RegistrationTimeout != Duration(5*time.Minute) {
			t.Errorf("expected garbage collection registration timeout default to be 5m, got %v", config.Shard.GarbageCollection.RegistrationTimeout)
		}

		err := config.Validate()
		if err != nil {
			t.Errorf("Valid config should not have validation errors: %v", err)
		}
	})

	t.Run("InvalidPort", func(t *testing.T) {
		config := &Config{
			Cluster: ClusterConfig{
				ID:      "example-cluster",
				Secrets: SecretsConfig{Provider: "memory"},
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
					RegistrationAddr: "0.0.0.0:0", // Invalid port
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

		config.SetDefaults()
		err := config.Validate()
		if err == nil {
			t.Error("Expected validation error for invalid port")
		}
	})

	t.Run("SamePortsError", func(t *testing.T) {
		config := &Config{
			Cluster: ClusterConfig{
				ID:      "example-cluster",
				Secrets: SecretsConfig{Provider: "memory"},
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
					OperatorAddr:     "0.0.0.0:8992", // Same as registration addr
					AgentAddr:        "0.0.0.0:8994",
				},
				Advertise: AdvertiseConfig{
					HealthAddr:       "172.16.0.1:8990",
					ElectionAddr:     "172.16.0.1:8991",
					RegistrationAddr: "172.16.0.1:8992",
					OperatorAddr:     "172.16.0.1:8992", // Same as registration addr
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

		config.SetDefaults()
		err := config.Validate()
		if err == nil {
			t.Error("Expected validation error for duplicate ports across services")
		}
	})

	t.Run("UnknownTemplateInGroup", func(t *testing.T) {
		config := &Config{
			Cluster: ClusterConfig{
				ID:      "example-cluster",
				Secrets: SecretsConfig{Provider: "memory"},
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
			Groups: map[string]map[string]GroupConfig{
				"default": {
					"badgroup": {
						Template: "nonexistent",
					},
				},
			},
		}

		config.SetDefaults()
		err := config.Validate()
		if err == nil {
			t.Error("Expected validation error for unknown template reference")
		}
	})

	t.Run("UnknownCertificateInTemplate", func(t *testing.T) {
		config := &Config{
			Cluster: ClusterConfig{
				ID:      "example-cluster",
				Secrets: SecretsConfig{Provider: "memory"},
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
					Files: map[string]FileConfig{
						"cert.pem": {
							Kind:     "certificate",
							Template: "nonexistent-cert",
						},
					},
				},
			},
		}

		config.SetDefaults()
		err := config.Validate()
		if err == nil {
			t.Error("Expected validation error for unknown certificate reference")
		}
	})
}

func TestConfigDefaults(t *testing.T) {
	config := &Config{
		Cluster: ClusterConfig{
			ID:      "example-cluster",
			Secrets: SecretsConfig{Provider: "memory"},
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
		Certificates: map[string]CertConfig{
			"test-cert": {
				Kind: "client",
			},
		},
	}

	config.SetDefaults()

	// Check shard defaults
	if config.Shard.RequestTimeout == 0 {
		t.Error("RequestTimeout default not set")
	}
	if config.Shard.HealthCheckInterval == 0 {
		t.Error("HealthCheckInterval default not set")
	}

	// Check certificate defaults
	if config.Certificates["test-cert"].TTL == 0 {
		t.Error("Certificate TTL default not set")
	}

	// Check template defaults
	template := config.Templates["test"]
	if template.Size == 0 {
		t.Error("Template size default not set")
	}
	// Subnet defaults to empty string - it's set explicitly in config, not via SetDefaults
	if template.Vars == nil {
		t.Error("Template vars should be initialized")
	}
}

func TestDeepMergeArgs(t *testing.T) {
	t.Run("SimpleMerge", func(t *testing.T) {
		dst := map[string]interface{}{
			"a": 1,
			"b": 2,
		}
		src := map[string]interface{}{
			"b": 3,
			"c": 4,
		}

		deepMergeArgs(dst, src)

		if dst["a"].(int) != 1 {
			t.Error("dst[a] should remain unchanged")
		}
		if dst["b"].(int) != 3 {
			t.Error("dst[b] should be overwritten by src")
		}
		if dst["c"].(int) != 4 {
			t.Error("dst[c] should be added from src")
		}
	})

	t.Run("NestedMerge", func(t *testing.T) {
		dst := map[string]interface{}{
			"a": 1,
			"b": map[string]interface{}{
				"hello":  "world",
				"always": "here",
			},
			"c": map[string]interface{}{
				"goodbye": "world",
			},
		}
		src := map[string]interface{}{
			"a": "2",
			"b": map[string]interface{}{
				"hello":    "there",
				"location": "world",
			},
			"c": 3,
		}

		deepMergeArgs(dst, src)

		// Check simple override
		if dst["a"].(string) != "2" {
			t.Error("dst[a] should be overwritten")
		}

		// Check nested merge
		bMap, ok := dst["b"].(map[string]interface{})
		if !ok {
			t.Fatal("dst[b] should be a map")
		}
		if bMap["hello"].(string) != "there" {
			t.Error("nested hello should be overwritten")
		}
		if bMap["always"].(string) != "here" {
			t.Error("nested always should remain")
		}
		if bMap["location"].(string) != "world" {
			t.Error("nested location should be added")
		}

		// Check object replacement
		if dst["c"].(int) != 3 {
			t.Error("dst[c] should be completely replaced")
		}
	})
}

func TestGetMergedConfig(t *testing.T) {
	config := &Config{
		Defaults: DefaultsConfig{
			Args: map[string]interface{}{
				"globalArg": "global",
			},
			Vars: map[string]string{
				"GLOBAL_VAR": "global",
				"OVERRIDE":   "global",
			},
			Userdata: &UserdataConfig{Content: "global userdata"},
		},
		Templates: map[string]TemplateConfig{
			"test": {
				Kind: "tst",
				Arch: "amd64",
				Args: map[string]interface{}{
					"templateArg": "template",
				},
				Vars: map[string]string{
					"TEMPLATE_VAR": "template",
					"OVERRIDE":     "template",
				},
				InstanceType: "t3.medium",
				SubnetPool:   "subnet-1",
				Files: map[string]FileConfig{
					"file1": {
						Kind:   "secret",
						Source: "secret1",
					},
					"cert1": {
						Kind:     "certificate",
						Template: "cert-config-1",
					},
				},
			},
		},
	}

	groupConfig := GroupConfig{
		Vars: map[string]string{
			"GROUP_VAR": "group",
			"OVERRIDE":  "group",
		},
		InstanceType: "t3.large",
		SubnetPool:   "subnet-3",
	}

	merged, err := config.GetMergedConfig("test", groupConfig)
	if err != nil {
		t.Fatalf("Failed to get merged config: %v", err)
	}

	// Check args merge
	if merged.Args["globalArg"].(string) != "global" {
		t.Error("Global args should be present")
	}
	if merged.Args["templateArg"].(string) != "template" {
		t.Error("Template args should be present")
	}

	// Check vars merge (group should override template, template should override global)
	if merged.Vars["GLOBAL_VAR"] != "global" {
		t.Error("Global var should be present")
	}
	if merged.Vars["TEMPLATE_VAR"] != "template" {
		t.Error("Template var should be present")
	}
	if merged.Vars["GROUP_VAR"] != "group" {
		t.Error("Group var should be present")
	}
	if merged.Vars["OVERRIDE"] != "group" {
		t.Error("Group var should override template and global")
	}

	// Check other fields
	if merged.Kind != "tst" {
		t.Error("Kind should come from template")
	}
	if merged.Arch != "amd64" {
		t.Error("Arch should come from template")
	}
	if merged.InstanceType != "t3.large" {
		t.Error("Instance type should be overridden by group")
	}
	if merged.SubnetPool != "subnet-3" {
		t.Error("Subnet should be overridden by group")
	}
}

func TestConfigClone(t *testing.T) {
	original := &Config{
		Cluster: ClusterConfig{
			ID:      "example-cluster",
			Secrets: SecretsConfig{Provider: "memory"},
		},
		Shard: ShardConfig{
			ID:            "test-shard",
			Infra:         InfraConfig{Provider: "aws", Region: "us-west-2", Zone: "us-west-2a"},
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
		Defaults: DefaultsConfig{
			Vars: map[string]string{
				"test": "value",
			},
		},
		Templates: map[string]TemplateConfig{
			"test": {
				Kind: "tst",
				Arch: "amd64",
			},
		},
	}

	cloned, err := original.Clone()
	if err != nil {
		t.Fatalf("Failed to clone config: %v", err)
	}

	// Verify values are equal
	if cloned.Shard.Bind.RegistrationAddr != original.Shard.Bind.RegistrationAddr {
		t.Error("Cloned shard bind config doesn't match")
	}
	if cloned.Defaults.Vars["test"] != original.Defaults.Vars["test"] {
		t.Error("Cloned vars don't match")
	}
	if cloned.Templates["test"].Kind != original.Templates["test"].Kind {
		t.Error("Cloned template doesn't match")
	}

	// Verify they are separate objects (modify clone shouldn't affect original)
	cloned.Defaults.Vars["test"] = "modified"
	if original.Defaults.Vars["test"] == "modified" {
		t.Error("Modifying clone affected original")
	}
}

func TestJSONSerialization(t *testing.T) {
	config := &Config{
		Cluster: ClusterConfig{
			ID:      "example-cluster",
			Secrets: SecretsConfig{Provider: "memory"},
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

	// Marshal to JSON
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	// Unmarshal back
	var unmarshaled Config
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal config: %v", err)
	}

	// Verify key fields
	if unmarshaled.Shard.Bind.RegistrationAddr != config.Shard.Bind.RegistrationAddr {
		t.Error("Shard bind config not preserved through JSON serialization")
	}
	if unmarshaled.Shard.Infra.Provider != config.Shard.Infra.Provider {
		t.Error("Infra config not preserved through JSON serialization")
	}
	if unmarshaled.Templates["test"].Kind != config.Templates["test"].Kind {
		t.Error("Template config not preserved through JSON serialization")
	}
}

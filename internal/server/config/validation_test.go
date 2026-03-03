// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvTemplateValidation(t *testing.T) {
	baseConfig := &Config{
		Cluster: ClusterConfig{
			ID:      "example-cluster",
			Secrets: SecretsConfig{Provider: "memory"},
		},
		Shard: ShardConfig{
			ID: "us-west-2a",
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
				Files: map[string]FileConfig{
					"valid.env": {
						Kind: "env",
						Template: map[string]interface{}{
							"VALID_KEY": "{{ .Instance.ID }}",
							"OTHER_KEY": "static-value",
						},
					},
					"invalid.env": {
						Kind: "env",
						Template: map[string]interface{}{
							"VALID_KEY":   "{{ .Instance.ID }}",
							"INVALID_KEY": 123, // This should fail validation
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name      string
		modifyFn  func(*Config)
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid env template with all string values",
			modifyFn: func(c *Config) {
				// Keep only the valid template
				delete(c.Templates["test"].Files, "invalid.env")
			},
			expectErr: false,
		},
		{
			name: "invalid env template with non-string value",
			modifyFn: func(c *Config) {
				// Keep only the invalid template
				delete(c.Templates["test"].Files, "valid.env")
			},
			expectErr: true,
			errMsg:    "env key INVALID_KEY must have string value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clone the base config
			config, err := baseConfig.Clone()
			if err != nil {
				t.Fatalf("Failed to clone config: %v", err)
			}

			// Apply modifications
			tt.modifyFn(config)

			// Set defaults before validation
			config.SetDefaults()

			// Test validation
			err = config.Validate()
			if tt.expectErr {
				if err == nil {
					t.Error("Expected validation error but got none")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Expected error containing %q, got: %v", tt.errMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no validation error but got: %v", err)
				}
			}
		})
	}
}

func TestValidateFile(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: `{
				"cluster": {
					"id": "example-cluster",
					"secrets": {
						"provider": "object-storage",
						"prefix": "secret/",
						"encryption_key": {
							"provider": "env",
							"source": "NSTANCE_ENCRYPTION_KEY"
						}
					}
				},
				"shard": {
					"id": "dev",
					"infra": {
						"provider": "mock",
						"region": "us-west-2",
						"zone": "us-west-2a"
					},
					"leader_network": {"ip": "172.16.0.100", "interface_id": "eni-test123"},
					"bind": {
						"health_addr": "0.0.0.0:8990",
						"election_addr": "0.0.0.0:8991",
						"registration_addr": "0.0.0.0:8992",
						"operator_addr": "0.0.0.0:8993",
						"agent_addr": "0.0.0.0:8994"
					},
					"advertise": {
						"health_addr": ":8990",
						"election_addr": ":8991",
						"registration_addr": "172.16.0.1:8992",
						"operator_addr": "172.16.0.1:8993",
						"agent_addr": "172.16.0.1:8994"
					},
					"subnet_pools": {
						"default": ["subnet-123"]
					}
				},
				"templates": {
					"test": {
						"kind": "tst",
						"arch": "amd64",
						"subnet_pool": "default"
					}
				}
			}`,
			wantErr: false,
		},
		{
			name: "invalid provider kind",
			config: `{
				"cluster": {
					"id": "cls123",
					"secrets": {"provider": "memory"}
				},
				"shard": {
					"id": "dev",
					"infra": {
						"provider": "invalid",
						"region": "us-west-2",
						"zone": "us-west-2a"
					}
				},
				"templates": {}
			}`,
			wantErr: true,
			errMsg:  "validation errors",
		},
		{
			name: "malformed json",
			config: `{
				"cluster": {
					"id": "cls123"
					"secrets": {"provider": "memory"}
				}
			}`,
			wantErr: true,
			errMsg:  "failed to parse configuration JSONC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary file
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test-config.jsonc")

			err := os.WriteFile(tmpFile, []byte(tt.config), 0644)
			if err != nil {
				t.Fatalf("Failed to write test config: %v", err)
			}

			// Test ValidateFile
			config, err := ValidateFile(tmpFile)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateFile() expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateFile() error = %v, want error containing %q", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateFile() unexpected error = %v", err)
				}
				if config == nil {
					t.Errorf("ValidateFile() expected config, got nil")
				}
			}
		})
	}
}

func TestValidateFile_FileNotFound(t *testing.T) {
	_, err := ValidateFile("non-existent-file.jsonc")
	if err == nil {
		t.Errorf("ValidateFile() expected error for non-existent file, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read config file") {
		t.Errorf("ValidateFile() error = %v, want error containing 'failed to read config file'", err)
	}
}

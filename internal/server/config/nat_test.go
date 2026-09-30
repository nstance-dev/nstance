// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
	"time"
)

// validNATConfig returns a valid configuration suitable for focused mutations.
func validNATConfig() *Config {
	c := &Config{
		Shard: ShardConfig{
			SubnetPools:        map[string][]string{"nodes-0": {"subnet-0"}, "nodes": {"subnet-1"}, "public": {"subnet-public"}},
			DynamicSubnetPools: []string{"nodes"},
		},
		Templates: map[string]TemplateConfig{
			"nat":  {Kind: "nat"},
			"node": {Kind: "knd", SubnetPool: "nodes"},
		},
		Groups: map[string]map[string]GroupConfig{
			"default": {
				"nat":     {Template: "nat", InstanceType: "small", SubnetPool: "public"},
				"workers": {Template: "node"},
			},
		},
		NAT: map[string]NATConfig{
			"default": {Group: "nat", InstanceTypeLadder: []string{"small", "large"}},
		},
	}
	c.SetDefaults()
	return c
}

// TestNATValidation covers valid configuration and invalid scaling settings.
func TestNATValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"valid", func(*Config) {}, ""},
		{"missing group", func(c *Config) { n := c.NAT["default"]; n.Group = ""; c.NAT["default"] = n }, "group is required"},
		{"unknown group", func(c *Config) { n := c.NAT["default"]; n.Group = "missing"; c.NAT["default"] = n }, "unknown group"},
		{"fixed size", func(c *Config) { g := c.Groups["default"]["nat"]; g.Size = IntPtr(0); c.Groups["default"]["nat"] = g }, "cannot specify size"},
		{"negative grace", func(c *Config) { n := c.NAT["default"]; n.LastInstanceGracePeriod = -1; c.NAT["default"] = n }, "grace"},
		{"ladder misses start", func(c *Config) {
			nat := c.NAT["default"]
			nat.InstanceTypeLadder = []string{"large"}
			c.NAT["default"] = nat
		}, "must include"},
		{"duplicate ladder", func(c *Config) {
			nat := c.NAT["default"]
			nat.InstanceTypeLadder = []string{"small", "small"}
			c.NAT["default"] = nat
		}, "duplicate"},
		{"short up window", func(c *Config) {
			nat := c.NAT["default"]
			nat.ScaleUpWindow = Duration(time.Minute)
			c.NAT["default"] = nat
		}, "between 2m and 5m"},
		{"long down window", func(c *Config) {
			nat := c.NAT["default"]
			nat.ScaleDownWindow = Duration(31 * time.Minute)
			c.NAT["default"] = nat
		}, "between 20m and 30m"},
		{"short cooldown", func(c *Config) {
			nat := c.NAT["default"]
			nat.Cooldown = Duration(9 * time.Minute)
			c.NAT["default"] = nat
		}, "at least 10m"},
		{"invalid threshold ordering", func(c *Config) {
			nat := c.NAT["default"]
			nat.ScaleDownThresholds.CPUPercent = 90
			c.NAT["default"] = nat
		}, "thresholds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validNATConfig()
			tt.mutate(c)
			err := c.validateNAT()
			if tt.want == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

// TestNATPublicAddressValidation verifies provider-specific fields and
// exclusive address ownership.
func TestNATPublicAddressValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"AWS address", func(c *Config) {
			c.Shard.Infra.Provider = "aws"
			n := c.NAT["default"]
			n.PublicAddresses = []PublicAddress{{IPv4: "192.0.2.1", AllocationID: "eipalloc-1"}}
			c.NAT["default"] = n
		}, ""},
		{"AWS missing allocation", func(c *Config) {
			c.Shard.Infra.Provider = "aws"
			n := c.NAT["default"]
			n.PublicAddresses = []PublicAddress{{IPv4: "192.0.2.1"}}
			c.NAT["default"] = n
		}, "requires allocation_id"},
		{"Google address", func(c *Config) {
			c.Shard.Infra.Provider = "google"
			n := c.NAT["default"]
			n.PublicAddresses = []PublicAddress{{IPv4: "192.0.2.1"}}
			c.NAT["default"] = n
		}, ""},
		{"Google allocation", func(c *Config) {
			c.Shard.Infra.Provider = "google"
			n := c.NAT["default"]
			n.PublicAddresses = []PublicAddress{{IPv4: "192.0.2.1", AllocationID: "eipalloc-1"}}
			c.NAT["default"] = n
		}, "cannot specify allocation_id"},
		{"invalid address", func(c *Config) {
			n := c.NAT["default"]
			n.PublicAddresses = []PublicAddress{{IPv4: "2001:db8::1"}}
			c.NAT["default"] = n
		}, "invalid ipv4"},
		{"shared address", func(c *Config) {
			n := c.NAT["default"]
			n.PublicAddresses = []PublicAddress{{IPv4: "192.0.2.1"}}
			c.NAT["default"] = n
			c.NAT["other"] = n
			c.Groups["other"] = c.Groups["default"]
		}, "used by tenants"},
		{"shared AWS allocation", func(c *Config) {
			c.Shard.Infra.Provider = "aws"
			n := c.NAT["default"]
			n.PublicAddresses = []PublicAddress{{IPv4: "192.0.2.1", AllocationID: "eipalloc-1"}}
			c.NAT["default"] = n
			n.PublicAddresses = []PublicAddress{{IPv4: "192.0.2.2", AllocationID: "eipalloc-1"}}
			c.NAT["other"] = n
			c.Groups["other"] = c.Groups["default"]
		}, "used by tenants"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validNATConfig()
			tt.mutate(c)
			err := c.validateNAT()
			if tt.want == "" && err != nil {
				t.Fatal(err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

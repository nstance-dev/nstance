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
			"default": {Group: "nat", NetworkIdentityPool: "nat-identities", InstanceTypeLadder: []string{"small", "large"}, SmallCluster: &SmallClusterNATConfig{InitialSubnet: "nodes-0", MaxInstances: 10}},
		},
	}
	c.SetDefaults()
	return c
}

// TestNATModesAndValidation covers valid mechanisms and invalid scaling settings.
func TestNATModesAndValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"both", func(*Config) {}, ""},
		{"group only", func(c *Config) {
			c.NAT["default"] = NATConfig{Group: "nat", NetworkIdentityPool: "ids", InstanceTypeLadder: []string{"small", "large"}}
			c.SetDefaults()
		}, ""},
		{"small cluster only", func(c *Config) {
			c.NAT["default"] = NATConfig{SmallCluster: &SmallClusterNATConfig{"nodes-0", 10}}
			c.SetDefaults()
		}, ""},
		{"no mechanism", func(c *Config) { c.NAT["default"] = NATConfig{LastNodeGracePeriod: Duration(time.Minute)} }, "must configure"},
		{"unknown group", func(c *Config) { n := c.NAT["default"]; n.Group = "missing"; c.NAT["default"] = n }, "unknown group"},
		{"fixed size", func(c *Config) { g := c.Groups["default"]["nat"]; g.Size = IntPtr(0); c.Groups["default"]["nat"] = g }, "cannot specify size"},
		{"missing network identity pool", func(c *Config) { n := c.NAT["default"]; n.NetworkIdentityPool = ""; c.NAT["default"] = n }, "network_identity_pool is required"},
		{"zero maximum", func(c *Config) { n := c.NAT["default"]; n.SmallCluster.MaxInstances = 0; c.NAT["default"] = n }, "max_instances"},
		{"negative grace", func(c *Config) { n := c.NAT["default"]; n.LastNodeGracePeriod = -1; c.NAT["default"] = n }, "grace"},
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

// TestNATSubnetReservation covers initial-subnet reservation and tenant exclusivity.
func TestNATSubnetReservation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"valid", func(*Config) {}, ""},
		{"unknown initial subnet", func(c *Config) { c.NAT["default"].SmallCluster.InitialSubnet = "missing" }, "unknown subnet"},
		{"dynamic allowlist", func(c *Config) { c.Shard.DynamicSubnetPools = append(c.Shard.DynamicSubnetPools, "nodes-0") }, "dynamic placement"},
		{"unrestricted dynamic", func(c *Config) { c.Shard.DynamicSubnetPools = nil }, "dynamic placement"},
		{"template collision", func(c *Config) { x := c.Templates["node"]; x.SubnetPool = "nodes-0"; c.Templates["node"] = x }, "used by template"},
		{"group collision", func(c *Config) {
			g := c.Groups["default"]["nat"]
			g.SubnetPool = "nodes-0"
			c.Groups["default"]["nat"] = g
		}, "used by group"},
		{"cross tenant collision", func(c *Config) {
			c.NAT["other"] = NATConfig{SmallCluster: &SmallClusterNATConfig{"nodes-0", 2}, LastNodeGracePeriod: Duration(time.Minute)}
		}, "multiple tenants"},
		{"cross tenant node subnet collision", func(c *Config) {
			c.Groups["other"] = map[string]GroupConfig{"workers": {Template: "node"}}
		}, "used by group"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validNATConfig()
			tt.mutate(c)
			err := c.ValidateSubnetConfig()
			if tt.want == "" && err != nil {
				t.Fatal(err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

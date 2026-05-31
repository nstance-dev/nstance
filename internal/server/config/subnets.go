// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"slices"

	"github.com/nstance-dev/nstance/internal/identifiers"
)

// ResolveSubnetKey resolves a subnet pool ID to its provider subnet IDs.
func (c *Config) ResolveSubnetKey(key string) ([]string, error) {
	if key == "" {
		return nil, nil
	}
	subnetIDs, exists := c.Shard.SubnetPools[key]
	if !exists {
		return nil, fmt.Errorf("unknown subnet pool ID %q (key not defined in shard.subnet_pools)", key)
	}
	return subnetIDs, nil
}

// ValidateDynamicSubnetKey validates that the given subnet pool ID is allowed for
// external requests (dynamic groups, per-instance overrides).
// Returns nil if allowed, error otherwise.
func (c *Config) ValidateDynamicSubnetKey(key string) error {
	if key == "" {
		return nil
	}
	if err := identifiers.Validate("subnet pool", key); err != nil {
		return fmt.Errorf("subnet pool ID %q: %w", key, err)
	}

	// First verify key exists
	if _, exists := c.Shard.SubnetPools[key]; !exists {
		return fmt.Errorf("unknown subnet pool ID %q (not defined in shard.subnet_pools)", key)
	}

	// If DynamicSubnetPools is empty, any key is allowed
	if len(c.Shard.DynamicSubnetPools) == 0 {
		return nil
	}

	// Check key is in DynamicSubnetPools
	if !slices.Contains(c.Shard.DynamicSubnetPools, key) {
		return fmt.Errorf("subnet pool ID %q is not allowed for external requests (allowed: %v)", key, c.Shard.DynamicSubnetPools)
	}

	return nil
}

// ValidateSubnetConfig validates the subnet configuration at config load time.
// Returns an error if validation fails.
func (c *Config) ValidateSubnetConfig() error {
	// Validate subnet map
	if len(c.Shard.SubnetPools) == 0 {
		return fmt.Errorf("shard.subnet_pools must be configured with at least one subnet pool")
	}

	// Track all subnet IDs to detect overlaps
	subnetIDToKey := make(map[string]string)

	for key, subnetIDs := range c.Shard.SubnetPools {
		if err := identifiers.Validate("subnet pool", key); err != nil {
			return fmt.Errorf("subnet pool ID %q: %w", key, err)
		}
		// Empty subnet list for a key
		if len(subnetIDs) == 0 {
			return fmt.Errorf("subnet pool ID %q has no associated subnet IDs", key)
		}

		// Check for overlapping subnet IDs across keys
		for _, subnetID := range subnetIDs {
			if existingKey, exists := subnetIDToKey[subnetID]; exists {
				return fmt.Errorf("provider subnet ID %q appears in multiple subnet pools: %q, %q", subnetID, existingKey, key)
			}
			subnetIDToKey[subnetID] = key
		}
	}

	// Validate DynamicSubnets references existing keys
	for _, dynamicKey := range c.Shard.DynamicSubnetPools {
		if err := identifiers.Validate("subnet pool", dynamicKey); err != nil {
			return fmt.Errorf("dynamic_subnet_pools subnet pool %q: %w", dynamicKey, err)
		}
		if _, exists := c.Shard.SubnetPools[dynamicKey]; !exists {
			return fmt.Errorf("dynamic_subnet_pools references unknown subnet pool %q (not in shard.subnet_pools)", dynamicKey)
		}
	}

	// Validate template subnet pools
	for templateName, template := range c.Templates {
		if template.SubnetPool != "" {
			if _, exists := c.Shard.SubnetPools[template.SubnetPool]; !exists {
				return fmt.Errorf("template %q references unknown subnet pool %q", templateName, template.SubnetPool)
			}
		}
	}

	// Validate static group subnet pools
	for tenant, tenantGroups := range c.Groups {
		for groupName, group := range tenantGroups {
			if group.SubnetPool != "" {
				if _, exists := c.Shard.SubnetPools[group.SubnetPool]; !exists {
					return fmt.Errorf("group %q (tenant %q) references unknown subnet pool %q", groupName, tenant, group.SubnetPool)
				}
			}
		}
	}

	return nil
}

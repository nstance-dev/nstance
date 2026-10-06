// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/nstance-dev/nstance/internal/identifiers"
)

// validateNAT validates tenant NAT mechanisms, references, and scaling settings.
func (c *Config) validateNAT() error {
	usedAddresses := make(map[netip.Addr]string)
	usedAllocations := make(map[string]string)
	for tenant, nat := range c.NAT {
		if err := identifiers.Validate("tenant ID", tenant); err != nil {
			return fmt.Errorf("nat tenant %q: %w", tenant, err)
		}
		if nat.Group == "" {
			return fmt.Errorf("nat tenant %q group is required", tenant)
		}
		if nat.Mode != "nat44" && nat.Mode != "nat64" {
			return fmt.Errorf("nat tenant %q mode must be nat44 or nat64", tenant)
		}
		if nat.LastInstanceGracePeriod <= 0 {
			return fmt.Errorf("nat tenant %q last_instance_grace_period must be positive", tenant)
		}
		if err := validateNATThresholds(nat.ScaleUpThresholds, nat.ScaleDownThresholds); err != nil {
			return fmt.Errorf("nat tenant %q: %w", tenant, err)
		}
		if err := c.validatePublicAddresses(tenant, nat.PublicAddresses, usedAddresses, usedAllocations); err != nil {
			return err
		}
		group, ok := c.Groups[tenant][nat.Group]
		if !ok {
			return fmt.Errorf("nat tenant %q references unknown group %q", tenant, nat.Group)
		}
		if group.Size != nil {
			return fmt.Errorf("nat group %q in tenant %q cannot specify size", nat.Group, tenant)
		}
		if group.InstanceType == "" {
			return fmt.Errorf("nat group %q in tenant %q requires instance_type", nat.Group, tenant)
		}
		if err := validateNATGroup(group.InstanceType, nat); err != nil {
			return fmt.Errorf("nat group %q in tenant %q: %w", nat.Group, tenant, err)
		}
	}
	return nil
}

// validatePublicAddresses validates optional provider resources for fixed egress.
func (c *Config) validatePublicAddresses(tenant string, addresses []PublicAddress, usedAddresses map[netip.Addr]string, usedAllocations map[string]string) error {
	for _, configured := range addresses {
		address, err := netip.ParseAddr(configured.IPv4)
		if err != nil || !address.Is4() {
			return fmt.Errorf("nat tenant %q public_addresses contains invalid ipv4 %q", tenant, configured.IPv4)
		}
		if owner := usedAddresses[address]; owner != "" {
			return fmt.Errorf("fixed public IPv4 address %q is used by tenants %q and %q", address, owner, tenant)
		}
		usedAddresses[address] = tenant
		switch c.Shard.Infra.Provider {
		case "aws":
			if configured.AllocationID == "" {
				return fmt.Errorf("AWS fixed public IPv4 address %q requires allocation_id", address)
			}
			if owner := usedAllocations[configured.AllocationID]; owner != "" {
				return fmt.Errorf("elastic IP allocation %q is used by tenants %q and %q", configured.AllocationID, owner, tenant)
			}
			usedAllocations[configured.AllocationID] = tenant
		case "google":
			if configured.AllocationID != "" {
				return fmt.Errorf("google cloud fixed public IPv4 address %q cannot specify allocation_id", address)
			}
		}
	}
	return nil
}

// validateNATGroup validates one dedicated NAT group's vertical-scaling policy.
func validateNATGroup(start string, cfg NATConfig) error {
	if len(cfg.InstanceTypeLadder) == 0 {
		return fmt.Errorf("instance_type_ladder must not be empty")
	}
	found, seen := false, make(map[string]struct{}, len(cfg.InstanceTypeLadder))
	for _, instanceType := range cfg.InstanceTypeLadder {
		if instanceType == "" {
			return fmt.Errorf("instance_type_ladder must not contain an empty value")
		}
		if _, ok := seen[instanceType]; ok {
			return fmt.Errorf("instance_type_ladder contains duplicate %q", instanceType)
		}
		seen[instanceType] = struct{}{}
		found = found || instanceType == start
	}
	if !found {
		return fmt.Errorf("instance_type_ladder must include starting instance_type %q", start)
	}
	if cfg.ScaleUpWindow < Duration(2*time.Minute) || cfg.ScaleUpWindow > Duration(5*time.Minute) {
		return fmt.Errorf("scale_up_window must be between 2m and 5m")
	}
	if cfg.ScaleDownWindow < Duration(20*time.Minute) || cfg.ScaleDownWindow > Duration(30*time.Minute) {
		return fmt.Errorf("scale_down_window must be between 20m and 30m")
	}
	if cfg.Cooldown < Duration(10*time.Minute) {
		return fmt.Errorf("cooldown must be at least 10m")
	}
	if cfg.ReplacementTimeout <= 0 {
		return fmt.Errorf("replacement_timeout must be positive")
	}
	return nil
}

// validateNATThresholds validates one tenant's scale-up and scale-down thresholds.
func validateNATThresholds(up, down NATThresholds) error {
	upValues := []float64{up.ConntrackPercent, up.CPUPercent}
	downValues := []float64{down.ConntrackPercent, down.CPUPercent}
	for i := range upValues {
		if upValues[i] <= 0 || upValues[i] > 100 || downValues[i] <= 0 || downValues[i] > 100 || downValues[i] >= upValues[i] {
			return fmt.Errorf("utilization thresholds must be in (0,100] with scale-down below scale-up")
		}
	}
	if up.PacketDropsPerSecond < 0 || down.PacketDropsPerSecond < 0 || down.PacketDropsPerSecond >= up.PacketDropsPerSecond {
		return fmt.Errorf("packet-drop thresholds must be non-negative with scale-down below scale-up")
	}
	return nil
}

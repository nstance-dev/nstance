// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"fmt"
	"maps"
)

// GetGroup retrieves a group by tenant and key, merging static and dynamic configs.
// Returns the final merged group configuration.
func GetGroup(ctx context.Context, loader *Loader, tenant, key string) (*GroupConfig, error) {
	if tenant == "" {
		return nil, fmt.Errorf("tenant is required")
	}

	config := loader.GetCurrent()
	staticTenantGroups := config.Groups[tenant]
	if staticTenantGroups == nil {
		staticTenantGroups = make(map[string]GroupConfig)
	}

	allDynamicGroups, err := loader.LoadDynamicGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load dynamic groups: %w", err)
	}

	dynamicGroups := allDynamicGroups[tenant]
	if dynamicGroups == nil {
		dynamicGroups = make(map[string]GroupConfig)
	}

	merged := mergeGroups(staticTenantGroups, dynamicGroups)

	group, exists := merged[key]
	if !exists {
		return nil, fmt.Errorf("group not found for key '%s' in tenant '%s'", key, tenant)
	}

	return &group, nil
}

// GetGroups returns all groups for a tenant, merging static and dynamic configs.
// Returns a map of final merged group configurations (map key is the group key).
func GetGroups(ctx context.Context, loader *Loader, tenant string) (map[string]GroupConfig, error) {
	if tenant == "" {
		return nil, fmt.Errorf("tenant is required")
	}

	config := loader.GetCurrent()
	staticTenantGroups := config.Groups[tenant]
	if staticTenantGroups == nil {
		staticTenantGroups = make(map[string]GroupConfig)
	}

	allDynamicGroups, err := loader.LoadDynamicGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load dynamic groups: %w", err)
	}

	dynamicGroups := allDynamicGroups[tenant]
	if dynamicGroups == nil {
		dynamicGroups = make(map[string]GroupConfig)
	}

	return mergeGroups(staticTenantGroups, dynamicGroups), nil
}

// TenantGroup represents a group with its tenant context
type TenantGroup struct {
	Tenant string
	Key    string
	Config GroupConfig
}

// GetAllGroups returns all groups across all tenants, merging static and dynamic configs
// Returns a slice of TenantGroup containing tenant, group key, and merged config
func GetAllGroups(ctx context.Context, loader *Loader) ([]TenantGroup, error) {
	config := loader.GetCurrent()

	allDynamicGroups, err := loader.LoadDynamicGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load dynamic groups: %w", err)
	}

	// Collect all tenant keys from both static and dynamic configs
	tenants := make(map[string]struct{})
	for tenant := range config.Groups {
		tenants[tenant] = struct{}{}
	}
	for tenant := range allDynamicGroups {
		tenants[tenant] = struct{}{}
	}

	var result []TenantGroup
	for tenant := range tenants {
		staticTenantGroups := config.Groups[tenant]
		if staticTenantGroups == nil {
			staticTenantGroups = make(map[string]GroupConfig)
		}

		dynamicGroups := allDynamicGroups[tenant]
		if dynamicGroups == nil {
			dynamicGroups = make(map[string]GroupConfig)
		}

		merged := mergeGroups(staticTenantGroups, dynamicGroups)
		for key, cfg := range merged {
			result = append(result, TenantGroup{
				Tenant: tenant,
				Key:    key,
				Config: cfg,
			})
		}
	}

	return result, nil
}

// GetConfigAndGroup returns the current configuration and effective group read together.
func GetConfigAndGroup(loader *Loader, tenant, key string) (*Config, *GroupConfig, error) {
	if tenant == "" {
		return nil, nil, fmt.Errorf("tenant is required")
	}
	loader.configMu.RLock()
	defer loader.configMu.RUnlock()
	loader.groupsMu.RLock()
	defer loader.groupsMu.RUnlock()

	if loader.config == nil {
		return nil, nil, fmt.Errorf("configuration not loaded")
	}
	if loader.groups == nil {
		return nil, nil, fmt.Errorf("dynamic groups not loaded")
	}
	group, exists := mergeGroups(loader.config.Groups[tenant], loader.groups[tenant])[key]
	if !exists {
		return nil, nil, fmt.Errorf("group not found for key '%s' in tenant '%s'", key, tenant)
	}
	return loader.config, &group, nil
}

// UpsertGroup creates or updates a group for a specific tenant.
// - If group exists in static config: validates only size/instanceType/vars are changed
// - If group exists in dynamic config: merges non-zero fields
// - If new group: validates all required fields are present, writes to dynamic config
func UpsertGroup(ctx context.Context, loader *Loader, tenant, key string, group GroupConfig) error {
	if tenant == "" {
		return fmt.Errorf("tenant is required")
	}

	c := loader.GetCurrent()
	staticTenantGroups := c.Groups[tenant]
	staticGroup, existsInStatic := staticTenantGroups[key]

	if err := loader.UpdateDynamicGroup(ctx, tenant, key, func(existingDynamic GroupConfig, existsInDynamic bool) (GroupConfig, error) {
		// Validate based on group type
		if existsInStatic {
			if group.Template != "" && group.Template != staticGroup.Template {
				return GroupConfig{}, fmt.Errorf("cannot override template for static group %s in tenant %s", key, tenant)
			}
			if group.SubnetPool != "" {
				return GroupConfig{}, fmt.Errorf("cannot override subnet pool for static group %s in tenant %s", key, tenant)
			}
			if len(group.Args) > 0 {
				return GroupConfig{}, fmt.Errorf("cannot override args for static group %s in tenant %s", key, tenant)
			}
			if group.DrainTimeout != nil {
				return GroupConfig{}, fmt.Errorf("cannot override drainTimeout for static group %s in tenant %s", key, tenant)
			}
		} else if !existsInDynamic {
			if group.Template == "" {
				return GroupConfig{}, fmt.Errorf("template is required for new group in tenant %s", tenant)
			}
			if group.SubnetPool == "" {
				template, exists := c.Templates[group.Template]
				if !exists {
					return GroupConfig{}, fmt.Errorf("template %s not found", group.Template)
				}
				if template.SubnetPool == "" {
					return GroupConfig{}, fmt.Errorf("template %s has no subnet pool configured", group.Template)
				}
				group.SubnetPool = template.SubnetPool
			}
		}

		if !existsInStatic && group.SubnetPool != "" {
			if err := c.ValidateDynamicSubnetKey(group.SubnetPool); err != nil {
				return GroupConfig{}, err
			}
		}
		return mergeGroupConfig(existingDynamic, group, existsInStatic), nil
	}); err != nil {
		return err
	}

	return nil
}

// mergeGroupConfig merges non-zero fields from update into existing.
// For static groups, only size/instanceType/vars can be set.
func mergeGroupConfig(existing, update GroupConfig, isStaticGroup bool) GroupConfig {
	if !isStaticGroup && update.Template != "" {
		existing.Template = update.Template
	}
	if update.Size != nil {
		existing.Size = update.Size
	}
	if update.InstanceType != "" {
		existing.InstanceType = update.InstanceType
	}
	if !isStaticGroup && update.SubnetPool != "" {
		existing.SubnetPool = update.SubnetPool
	}
	if len(update.Vars) > 0 {
		if existing.Vars == nil {
			existing.Vars = make(map[string]string)
		}
		for k, v := range update.Vars {
			existing.Vars[k] = v
		}
	}
	if !isStaticGroup && len(update.Args) > 0 {
		if existing.Args == nil {
			existing.Args = make(map[string]interface{})
		}
		for k, v := range update.Args {
			existing.Args[k] = v
		}
	}
	return existing
}

// DeleteGroup removes a dynamic group for a specific tenant.
// If group exists in static config, only the dynamic overrides are removed.
func DeleteGroup(ctx context.Context, loader *Loader, tenant, key string) error {
	if tenant == "" {
		return fmt.Errorf("tenant is required")
	}

	if err := loader.DeleteDynamicGroup(ctx, tenant, key); err != nil {
		return err
	}

	return nil
}

// mergeGroups merges static and dynamic groups
// For groups in both: template and subnet from static, size/instanceType/vars from dynamic
// Groups only in static: return static as-is
// Groups only in dynamic: return dynamic as-is
func mergeGroups(static, dynamic map[string]GroupConfig) map[string]GroupConfig {
	merged := make(map[string]GroupConfig)

	// Start with all static groups
	for key, staticGroup := range static {
		merged[key] = staticGroup
	}

	// Apply dynamic overrides
	for key, dynamicGroup := range dynamic {
		if staticGroup, existsInStatic := static[key]; existsInStatic {
			// Merge: keep template and subnet from static, override size/instanceType/vars from dynamic
			mergedGroup := staticGroup
			mergedGroup.Vars = maps.Clone(staticGroup.Vars)
			if dynamicGroup.Size != nil {
				mergedGroup.Size = dynamicGroup.Size
			}
			if dynamicGroup.InstanceType != "" {
				mergedGroup.InstanceType = dynamicGroup.InstanceType
			}
			if len(dynamicGroup.Vars) > 0 {
				if mergedGroup.Vars == nil {
					mergedGroup.Vars = make(map[string]string)
				}
				for k, v := range dynamicGroup.Vars {
					mergedGroup.Vars[k] = v
				}
			}
			// Keep drainTimeout from static group (cannot be overridden)
			merged[key] = mergedGroup
		} else {
			// Dynamic-only group
			merged[key] = dynamicGroup
		}
	}

	return merged
}

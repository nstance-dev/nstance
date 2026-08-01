// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"errors"
	"fmt"
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/instances"
)

// handleGroupChanged reconciles a specific group to its desired size
func (r *Reconciler) handleGroupChanged(tenant, groupKey string) error {
	r.logger.Info("Reconciling group", "tenant", tenant, "group", groupKey)

	// Get current instances for this group from localDB
	currentInstances, err := r.getGroupInstances(tenant, groupKey)
	if err != nil {
		return fmt.Errorf("failed to get current instances for group %s: %w", groupKey, err)
	}
	currentCount := len(currentInstances)

	// Get group configuration
	group, err := config.GetGroup(r.ctx, r.configLoader, tenant, groupKey)
	if err != nil {
		// Group was deleted - scale down all instances to 0
		r.logger.Info("Group not found, scaling down to 0", "tenant", tenant, "group", groupKey, "current", currentCount)
		r.cancelGroupExpiryTimer(tenant, groupKey)
		if currentCount > 0 {
			return r.scaleDown(tenant, groupKey, currentInstances, currentCount)
		}
		return nil
	}

	desiredCount := group.GetSize()

	r.logger.Info("Group reconciliation status",
		"group", groupKey,
		"current", currentCount,
		"desired", desiredCount)

	// Scale up if needed
	if currentCount < desiredCount {
		if err := r.scaleUp(tenant, groupKey, *group, desiredCount-currentCount); err != nil {
			return err
		}
	}

	// Scale down if needed
	if currentCount > desiredCount {
		if err := r.scaleDown(tenant, groupKey, currentInstances, currentCount-desiredCount); err != nil {
			return err
		}
	}

	// Check for infra config drift and rotate drifted instances (one at a time)
	r.checkGroupConfigDrift(tenant, groupKey, *group)

	// Check for instance expiry and schedule next expiry timer
	r.checkGroupExpiry(tenant, groupKey)
	r.scheduleGroupExpiry(tenant, groupKey)
	return nil
}

// scaleUp creates new instances for a group
func (r *Reconciler) scaleUp(tenant, groupKey string, group config.GroupConfig, count int) error {
	r.logger.Info("Scaling up group", "group", groupKey, "count", count)

	for i := 0; i < count; i++ {
		if _, err := r.createInstanceForGroup(tenant, groupKey, group, false); err != nil {
			if r.notifyError != nil {
				r.notifyError(tenant, groupKey, "", fmt.Sprintf("Failed to create instance: %v", err))
			}
			return fmt.Errorf("failed to create instance for group %s: %w", groupKey, err)
		}
	}
	return nil
}

// scaleDown deletes instances from a group (oldest first)
func (r *Reconciler) scaleDown(tenant, groupKey string, instances []string, count int) error {
	r.logger.Info("Scaling down group", "group", groupKey, "count", count)

	// Delete oldest instances first
	var errs []error
	for i := 0; i < count && i < len(instances); i++ {
		instanceID := instances[i]

		err := r.instanceManager.DeleteInstance(r.ctx, tenant, instanceID)
		if err != nil {
			r.logger.Error("Failed to delete instance",
				"group", groupKey,
				"instance_id", instanceID,
				"error", err)
			if r.notifyError != nil {
				r.notifyError(tenant, groupKey, instanceID, fmt.Sprintf("Failed to delete instance: %v", err))
			}
			errs = append(errs, fmt.Errorf("delete instance %s: %w", instanceID, err))
			continue
		}

		r.logger.Info("Deleted instance",
			"group", groupKey,
			"instance_id", instanceID)
	}
	return errors.Join(errs...)
}

// getGroupInstances returns all managed instances for a group (ordered by creation time, oldest first)
// Excludes on-demand instances
func (r *Reconciler) getGroupInstances(tenant, groupKey string) ([]string, error) {
	return r.localDB.GetInstancesByGroup(tenant, groupKey, true) // exclude on-demand
}

// checkGroupConfigDrift checks for infra config drift and triggers rotation for drifted instances.
// Only one instance is rotated at a time per group (skipped if any instance is already draining).
func (r *Reconciler) checkGroupConfigDrift(tenant, groupKey string, group config.GroupConfig) {
	dbGroup, err := r.configLoader.GetCachedGroup(tenant, groupKey)
	if err != nil {
		r.logger.Error("Failed to get group for config drift check", "group", groupKey, "error", err)
		return
	}
	if dbGroup == nil || dbGroup.InfraConfigHash == nil {
		return
	}

	currentInstances, err := r.getGroupInstances(tenant, groupKey)
	if err != nil {
		r.logger.Error("Failed to get instances for config drift check", "group", groupKey, "error", err)
		return
	}

	var driftedInstanceID string
	for _, instanceID := range currentInstances {
		instance, err := r.localDB.GetInstance(instanceID)
		if err != nil {
			r.logger.Warn("Failed to get instance for config drift check", "instance_id", instanceID, "error", err)
			continue
		}

		// If any instance is already draining, skip rotation
		if instance.DrainStartedAt != nil {
			return
		}

		// Track first drifted instance (oldest first since getGroupInstances is ordered by creation time)
		if driftedInstanceID == "" {
			instanceHash := ""
			if instance.InfraConfigHash != nil {
				instanceHash = *instance.InfraConfigHash
			}
			if instanceHash != *dbGroup.InfraConfigHash {
				driftedInstanceID = instanceID
			}
		}
	}

	if driftedInstanceID != "" {
		r.logger.Info("Rotating instance due to infra config drift",
			"instance_id", driftedInstanceID,
			"group", groupKey)
		r.replaceAndDrain(driftedInstanceID, tenant, groupKey, "config-drift", group, false)
	}
}

// maxOversizePadding is the maximum number of extra instances allowed during replacement/drain
const maxOversizePadding = 1

// createInstanceForGroup creates a single instance for a group (serialized with rate limiting).
// Set allowOversize to true when creating a replacement for an instance being drained.
func (r *Reconciler) createInstanceForGroup(tenant, groupKey string, group config.GroupConfig, allowOversize bool) (*instances.CreateInstanceResponse, error) {
	// Lock to serialize all instance creation and apply rate limiting
	r.createMu.Lock()
	defer r.createMu.Unlock()

	// Safety check: ensure we're not exceeding the group size
	// When allowOversize is true (during replacement), allow up to 1 extra instance
	currentCount, err := r.localDB.GetInstanceCountByGroup(tenant, groupKey, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get current instance count: %w", err)
	}
	maxSize := group.GetSize()
	if allowOversize {
		maxSize += maxOversizePadding
	}
	if currentCount >= maxSize {
		return nil, fmt.Errorf("group already at or above max size (current: %d, max: %d)", currentCount, maxSize)
	}

	// Apply rate limiting
	if r.createRateLimit > 0 {
		elapsed := time.Since(r.lastCreateTime)
		if elapsed < r.createRateLimit {
			sleep := r.createRateLimit - elapsed
			r.logger.Debug("Rate limiting create operation", "sleep", sleep)
			time.Sleep(sleep)
		}
	}

	// Create instance request
	req := instances.CreateInstanceRequest{
		Tenant:       tenant,
		Group:        groupKey,
		Template:     group.Template,
		InstanceType: group.InstanceType,
		SubnetPool:   group.SubnetPool,
		Vars:         group.Vars,
		Args:         group.Args,
		OnDemand:     false, // Managed instances created by reconciler
	}

	// Create instance
	resp, err := r.instanceManager.CreateInstance(r.ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create instance: %w", err)
	}

	// Update last create time
	r.lastCreateTime = time.Now().UTC()

	r.logger.Info("Created instance",
		"group", groupKey,
		"instance_id", resp.InstanceID,
		"provider_id", resp.ProviderInstanceID)

	return resp, nil
}

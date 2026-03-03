// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"fmt"

	"github.com/nstance-dev/nstance/internal/server/config"
)

// handleSpotTerminating processes spot instance termination notices
func (r *Reconciler) handleSpotTerminating(instanceID string) error {
	r.logger.Info("Handling spot termination for instance", "instance_id", instanceID)

	// Get instance details from localDB
	instance, err := r.localDB.GetInstance(instanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance %s from localDB: %w", instanceID, err)
	}

	groupKey := instance.Group

	// Get group configuration
	group, err := config.GetGroup(r.ctx, r.configLoader, instance.Tenant, groupKey)
	if err != nil {
		return fmt.Errorf("failed to get group configuration for %s: %w", groupKey, err)
	}

	// Determine effective drain timeout (group override or server default)
	cfg := r.configLoader.GetCurrent()
	drainTimeout := cfg.Shard.DefaultDrainTimeout
	if group.DrainTimeout != nil {
		drainTimeout = *group.DrainTimeout
	}

	// Check if drain coordination is needed
	if drainTimeout > 0 {
		r.initiateInstanceDrain(instanceID, groupKey, "spot-terminating", drainTimeout.Duration(), instance.DrainStartedAt != nil)
	}

	// Create replacement instance
	resp, err := r.createInstanceForGroup(instance.Tenant, groupKey, *group, true)
	if err != nil {
		if r.notifyError != nil {
			r.notifyError(groupKey, instanceID, fmt.Sprintf("Failed to create replacement for spot-terminating instance: %v", err))
		}
		return fmt.Errorf("failed to create replacement for spot-terminating instance %s: %w", instanceID, err)
	}

	r.logger.Info("Created replacement for spot terminating instance",
		"group", groupKey,
		"old_instance_id", instanceID,
		"new_instance_id", resp.InstanceID,
		"provider_id", resp.ProviderInstanceID)
	return nil
}

// handleInstanceDeleted processes an instance deletion event
func (r *Reconciler) handleInstanceDeleted(instanceID, tenant, groupKey string) error {
	r.logger.Info("Instance deleted event", "instance_id", instanceID, "tenant", tenant, "group", groupKey)

	// If group key and tenant are provided, reconcile the group to backfill
	if tenant != "" && groupKey != "" {
		return r.handleGroupChanged(tenant, groupKey)
	}
	return nil
}

// handleDrainAcked processes drain acknowledgment from operator
// Always proceeds with deletion, trusting operator regardless of tracked state
func (r *Reconciler) handleDrainAcked(instanceID string) error {
	r.logger.Info("Drain acknowledged by operator", "instance_id", instanceID)

	// Mark drain acked in database
	if err := r.localDB.MarkDrainAcked(instanceID); err != nil {
		r.logger.Warn("Failed to mark drain acked",
			"instance_id", instanceID,
			"error", err)
		// Continue anyway - operator said it's drained
	}

	// Operator has acknowledged drain is complete - proceed with deletion
	// This applies to both unhealthy instances and expiry instances
	r.logger.Info("Proceeding with deletion for acknowledged drain", "instance_id", instanceID)
	if err := r.instanceManager.DeleteInstance(r.ctx, instanceID); err != nil {
		if r.notifyError != nil {
			if instance, _ := r.localDB.GetInstance(instanceID); instance != nil {
				r.notifyError(instance.Group, instanceID, fmt.Sprintf("Failed to delete drained instance: %v", err))
			}
		}
		return fmt.Errorf("failed to delete drained instance %s: %w", instanceID, err)
	}

	r.logger.Info("Deleted drained instance", "instance_id", instanceID)
	return nil
}

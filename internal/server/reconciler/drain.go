// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
)

// replaceAndDrain creates a replacement instance and initiates drain on the old one.
// Used by both unhealthy replacement and instance expiry flows.
//
// When skipDrain is true, the old instance is deleted immediately after the
// replacement is created, bypassing drain coordination entirely. This is used
// when the provider reports the instance as stopping/stopped/deleting/deleted/failed
// or not found, since there is no running workload to drain.
func (r *Reconciler) replaceAndDrain(instanceID, tenant, groupKey, reason string, group config.GroupConfig, skipDrain bool) {
	cfg := r.configLoader.GetCurrent()
	drainTimeout := cfg.Shard.DefaultDrainTimeout
	if group.DrainTimeout != nil {
		drainTimeout = *group.DrainTimeout
	}

	// Create replacement first, so cluster has capacity for drain (allow oversize during replacement)
	resp, err := r.createInstanceForGroup(tenant, groupKey, group, true)
	if err != nil {
		r.logger.Error("Failed to create replacement instance",
			"group", groupKey,
			"old_instance_id", instanceID,
			"reason", reason,
			"error", err)
		return
	}
	r.logger.Info("Created replacement instance",
		"group", groupKey,
		"old_instance_id", instanceID,
		"new_instance_id", resp.InstanceID,
		"reason", reason)

	// Skip drain if instance is stopping/stopped/deleting/deleted/failed or drain timeout is 0
	if skipDrain || drainTimeout == 0 {
		r.deleteInstance(tenant, instanceID)
		return
	}

	// Initiate drain and schedule deletion check
	r.initiateInstanceDrain(instanceID, groupKey, reason, drainTimeout.Duration(), false)
	r.scheduleEvent(drainTimeout.Duration(), ReconcileEvent{
		Type:       EventCheckInstance,
		InstanceID: instanceID,
		Timestamp:  time.Now().UTC(),
	})
}

// initiateInstanceDrain marks an instance for drain and notifies the operator.
// Returns true if drain was initiated, false if already draining or if an error occurs.
func (r *Reconciler) initiateInstanceDrain(instanceID, groupKey, reason string, drainTimeout time.Duration, alreadyDraining bool) bool {
	// Check if already draining
	if alreadyDraining {
		r.logger.Info("Instance already marked for drain", "instance_id", instanceID)
		return false
	}

	// Mark drain started
	if err := r.localDB.MarkDrainStarted(instanceID); err != nil {
		r.logger.Error("Failed to mark drain started",
			"instance_id", instanceID,
			"error", err)
		return false
	}

	now := time.Now().UTC()
	r.logger.Info("Marked instance for drain",
		"instance_id", instanceID,
		"reason", reason,
		"timeout", drainTimeout)

	// Notify operator
	r.notifyDrain(instanceID, groupKey, reason, now, now.Add(drainTimeout))
	return true
}

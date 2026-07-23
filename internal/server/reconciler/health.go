// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/infra"
	"github.com/nstance-dev/nstance/internal/server/infra/provider"
	"github.com/nstance-dev/nstance/internal/server/localdb"
)

// providerStatusTimeout is the maximum time to wait for a provider status check
const providerStatusTimeout = 5 * time.Second

// handleCheckInstance checks instance health via provider and replaces if unhealthy
// This is triggered by health timeouts, gRPC disconnects, or drain timeout expiry
func (r *Reconciler) handleCheckInstance(event ReconcileEvent) {
	instanceID := event.InstanceID

	// Check if instance was intentionally deleted (soft-deleted in localdb)
	// This prevents attempting replacement during scale-down or explicit deletion
	instance, err := r.localDB.GetInstance(instanceID)
	if err != nil || instance.DeletedAt != nil {
		r.logger.Debug("Skipping health check for deleted instance", "instance_id", instanceID)
		return
	}

	// Check if drain is in progress and should be completed
	if instance.DrainStartedAt != nil {
		groupKey := instance.Group
		group, err := config.GetGroup(r.ctx, r.configLoader, instance.Tenant, groupKey)
		if err != nil {
			r.logger.Error("Failed to get group configuration", "tenant", instance.Tenant, "group", groupKey, "error", err)
			return
		}

		cfg := r.configLoader.GetCurrent()
		drainTimeout := cfg.Shard.DefaultDrainTimeout
		if group.DrainTimeout != nil {
			drainTimeout = *group.DrainTimeout
		}

		r.handleDrainCompletion(instanceID, instance, drainTimeout.Duration())
		return
	}

	// Skip health check if instance hasn't been provisioned yet
	if instance.ProviderID == nil || *instance.ProviderID == "" {
		r.logger.Debug("Instance has no provider ID yet, skipping health check", "instance_id", instanceID)
		return
	}

	// Check provider status to determine if instance is unhealthy
	r.logger.Info("Checking instance health", "instance_id", instanceID, "provider_id", *instance.ProviderID)
	statusCtx, statusCancel := context.WithTimeout(r.ctx, providerStatusTimeout)
	defer statusCancel()
	status, err := r.provider.GetInstanceStatus(statusCtx, instanceID, *instance.ProviderID)
	if err != nil {
		if errors.Is(err, provider.ErrInstanceNotFound) {
			// Instance was deleted externally (e.g., terminated in cloud provider console)
			// Mark instance as terminated in DB so it's excluded from instance counts
			terminatedState := &provider.InstanceStatus{Status: "terminated"}
			if stateJSON, jsonErr := json.Marshal(terminatedState); jsonErr == nil {
				if dbErr := r.localDB.UpdateInstanceProviderState(instanceID, stateJSON); dbErr != nil {
					r.logger.Warn("Failed to update provider state for terminated instance",
						"instance_id", instanceID,
						"error", dbErr)
				}
			}
			r.logger.Warn("Instance not found in provider (e.g. externally deleted), replacing",
				"instance_id", instanceID,
				"provider_id", *instance.ProviderID)
			r.replaceInstance(instanceID, true)
			return
		}
		if r.ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			r.logger.Error("Provider status check timed out",
				"instance_id", instanceID,
				"provider_id", *instance.ProviderID,
				"timeout", providerStatusTimeout)
		} else {
			r.logger.Error("Failed to get instance status from provider",
				"instance_id", instanceID,
				"provider_id", *instance.ProviderID,
				"error", err)
		}
		if r.notifyError != nil {
			r.notifyError(instance.Tenant, instance.Group, instanceID, fmt.Sprintf("Failed to get instance status: %v", err))
		}
		// Schedule retry with backoff so provider timeouts don't silently stall replacement
		r.scheduleDisconnectFollowUp(event, instance, instanceID, "unknown")
		return
	}

	r.logger.Info("Provider status check complete",
		"instance_id", instanceID,
		"status", status.Status,
		"health_at_set", instance.HealthAt != nil)

	// If instance is unhealthy according to provider, replace it.
	// Drain is skipped because the VM is already in a non-running state
	// (stopping/stopped/suspending/suspended/deleting/deleted/repairing),
	// so there are no active workloads to migrate. Drain is only used for
	// proactive replacement (spot termination, instance expiry) where the
	// VM is still running.
	if infra.IsUnhealthy(status.Status) {
		// Update provider state in DB before replacing so instance counts exclude this instance
		if stateJSON, jsonErr := json.Marshal(status); jsonErr == nil {
			if dbErr := r.localDB.UpdateInstanceProviderState(instanceID, stateJSON); dbErr != nil {
				r.logger.Warn("Failed to update provider state for unhealthy instance",
					"instance_id", instanceID,
					"error", dbErr)
			}
		}
		r.logger.Warn("Instance is unhealthy, replacing",
			"instance_id", instanceID,
			"status", status.Status)
		r.replaceInstance(instanceID, true)
		return
	}

	// Provider reports running, but check for missed health reports.
	// If the agent has missed 3+ consecutive health report intervals,
	// the instance is considered unhealthy despite the VM still running
	// (e.g. agent crashed but VM stayed up). Drain is NOT skipped here
	// because the VM is still running with workloads that should be
	// migrated before deletion.
	if instance.HealthAt != nil {
		cfg := r.configLoader.GetCurrent()
		healthInterval := cfg.Shard.HealthCheckInterval.Duration()
		if healthInterval > 0 {
			missedIntervals := int(time.Since(*instance.HealthAt) / healthInterval)
			if missedIntervals >= 3 {
				r.logger.Warn("Instance missed 3+ health reports but provider reports running, replacing",
					"instance_id", instanceID,
					"status", status.Status,
					"missed_intervals", missedIntervals,
					"last_health_at", instance.HealthAt)
				r.replaceInstance(instanceID, false)
				return
			}
		}
	}

	// If this check was triggered by a disconnect but provider still reports running
	// (e.g. ACPI shutdown in progress), schedule a follow-up check with exponential backoff
	if event.Cause == "disconnect" {
		r.scheduleDisconnectFollowUp(event, instance, instanceID, status.Status)
		return
	}

	r.logger.Debug("Instance healthy according to provider",
		"instance_id", instanceID,
		"status", status.Status)
}

// disconnectFollowUpPolicy is the retry policy for disconnect follow-up polling.
// Uses fast-poll backoff: 3s, 6s, 12s, 24s, 30s, 30s, 30s, 30s (capped at 30s).
var disconnectFollowUpPolicy = RetryPolicy{
	BaseDelay:   3 * time.Second,
	MaxDelay:    30 * time.Second,
	MaxAttempts: 8,
	JitterFrac:  0.25,
}

// scheduleDisconnectFollowUp schedules a follow-up provider check after a disconnect
// when the provider still reports the instance as running (e.g. ACPI shutdown in progress).
func (r *Reconciler) scheduleDisconnectFollowUp(event ReconcileEvent, instance *localdb.Instance, instanceID, providerStatus string) {
	// If agent reconnected since the disconnect, stop polling
	if instance.HealthAt != nil && instance.HealthAt.After(event.Timestamp) {
		r.logger.Info("Agent reconnected since disconnect, stopping follow-up polling",
			"instance_id", instanceID,
			"disconnect_at", event.Timestamp,
			"reconnect_at", instance.HealthAt)
		return
	}

	if event.Attempt >= disconnectFollowUpPolicy.MaxAttempts {
		r.logger.Warn("Max disconnect follow-up attempts reached, deferring to GC",
			"instance_id", instanceID,
			"attempts", event.Attempt,
			"status", providerStatus)
		return
	}

	delay := backoffDelay(disconnectFollowUpPolicy, event.Attempt)

	r.logger.Info("Scheduling follow-up provider check",
		"instance_id", instanceID,
		"status", providerStatus,
		"attempt", event.Attempt+1,
		"delay", delay)

	time.AfterFunc(delay, func() {
		r.Enqueue(ReconcileEvent{
			Type:             EventCheckInstance,
			InstanceID:       instanceID,
			Timestamp:        event.Timestamp,
			PreventDuplicate: false,
			Cause:            "disconnect",
			Attempt:          event.Attempt + 1,
		})
	})
}

// replaceInstance replaces an unhealthy instance.
//
// When skipDrain is true (instance is terminal/not-found in provider), drain is
// skipped because there is no running workload to migrate. The old instance is
// deleted immediately after the replacement is created.
//
// When skipDrain is false, the standard drain flow is followed:
//  1. Creates replacement instance immediately (allows desired+1 temporarily)
//  2. Marks drain_started_at in SQLite
//  3. Notifies operator via WatchInstanceEvents stream
//  4. Schedules deletion check after drain timeout
//  5. Deletes old instance after drain ack OR timeout
//
// For groups with drainTimeout = 0, the old instance is always deleted immediately.
//
// Idempotency: Drain notifications may be sent multiple times (e.g., leadership changes).
// Operator must handle duplicates. Server always honors AcknowledgeDrained regardless of state.
func (r *Reconciler) replaceInstance(instanceID string, skipDrain bool) {
	// Get instance details from localDB
	instance, err := r.localDB.GetInstance(instanceID)
	if err != nil {
		r.logger.Error("Failed to get instance from localDB", "instance_id", instanceID, "error", err)
		return
	}

	groupKey := instance.Group
	// Get group configuration
	group, err := config.GetGroup(r.ctx, r.configLoader, instance.Tenant, groupKey)
	if err != nil {
		r.logger.Error("Failed to get group configuration", "tenant", instance.Tenant, "group", groupKey, "error", err)
		return
	}

	// Check if drain already in progress (replacement already created)
	if instance.DrainStartedAt != nil {
		cfg := r.configLoader.GetCurrent()
		drainTimeout := cfg.Shard.DefaultDrainTimeout
		if group.DrainTimeout != nil {
			drainTimeout = *group.DrainTimeout
		}
		r.handleDrainCompletion(instanceID, instance, drainTimeout.Duration())
		return
	}

	r.logger.Info("Replacing unhealthy instance", "instance_id", instanceID, "group", groupKey, "skip_drain", skipDrain)
	r.replaceAndDrain(instanceID, instance.Tenant, groupKey, "unhealthy", *group, skipDrain)
}

// handleDrainCompletion checks if drain is complete and deletes the instance
func (r *Reconciler) handleDrainCompletion(instanceID string, instance *localdb.Instance, drainTimeout time.Duration) {
	elapsed := time.Since(*instance.DrainStartedAt)

	// Check if drain was acknowledged
	if instance.DrainAckedAt != nil {
		r.logger.Info("Drain acknowledged, proceeding with deletion",
			"instance_id", instanceID,
			"elapsed", elapsed)
		r.deleteInstance(instance.Tenant, instanceID)
		return
	}

	// Check if timeout elapsed
	if elapsed >= drainTimeout {
		r.logger.Warn("Drain timeout elapsed without acknowledgment, proceeding with deletion",
			"instance_id", instanceID,
			"elapsed", elapsed,
			"timeout", drainTimeout)
		r.deleteInstance(instance.Tenant, instanceID)
		return
	}

	// Check if instance is in a terminal state (e.g. stopped/deleted/failed)
	if instance.ProviderID != nil && *instance.ProviderID != "" {
		statusCtx, statusCancel := context.WithTimeout(r.ctx, providerStatusTimeout)
		defer statusCancel()
		status, err := r.provider.GetInstanceStatus(statusCtx, instanceID, *instance.ProviderID)
		if err != nil {
			if errors.Is(err, provider.ErrInstanceNotFound) {
				r.logger.Info("Draining instance not found in provider, proceeding with deletion",
					"instance_id", instanceID,
					"elapsed", elapsed)
				r.deleteInstance(instance.Tenant, instanceID)
				return
			}
			r.logger.Warn("Failed to check provider status for draining instance",
				"instance_id", instanceID,
				"error", err)
		} else if infra.IsUnhealthy(status.Status) {
			r.logger.Info("Draining instance is terminal, proceeding with deletion",
				"instance_id", instanceID,
				"status", status.Status,
				"elapsed", elapsed)
			r.deleteInstance(instance.Tenant, instanceID)
			return
		}
	}

	// Still within drain timeout, wait
	r.logger.Debug("Waiting for drain acknowledgment",
		"instance_id", instanceID,
		"elapsed", elapsed,
		"timeout", drainTimeout)
}

// deleteInstance deletes a tenant-owned instance and logs the result.
func (r *Reconciler) deleteInstance(tenant, instanceID string) {
	err := r.instanceManager.DeleteInstance(r.ctx, tenant, instanceID)
	if err != nil {
		r.logger.Error("Failed to delete instance",
			"instance_id", instanceID,
			"error", err)
		if r.notifyError != nil {
			if instance, _ := r.localDB.GetInstance(instanceID); instance != nil {
				r.notifyError(instance.Tenant, instance.Group, instanceID, fmt.Sprintf("Failed to delete instance: %v", err))
			}
		}
	} else {
		r.logger.Info("Deleted instance", "instance_id", instanceID)
	}
}

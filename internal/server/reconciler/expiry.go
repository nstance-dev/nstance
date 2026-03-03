// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"fmt"
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
)

const expiryTimerPadding = time.Second

// handleInitialReconcile reconciles all groups on startup or when becoming leader
func (r *Reconciler) handleInitialReconcile() error {
	r.logger.Info("Starting initial reconciliation")

	groups, err := config.GetAllGroups(r.ctx, r.configLoader)
	if err != nil {
		return fmt.Errorf("failed to get groups for initial reconciliation: %w", err)
	}

	var firstErr error
	for _, tg := range groups {
		if err := r.handleGroupChanged(tg.Tenant, tg.Key); err != nil {
			r.logger.Error("Failed to reconcile group during initial reconciliation",
				"tenant", tg.Tenant, "group", tg.Key, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	// Check for on-demand instance expiry
	r.checkOnDemandExpiry()

	r.logger.Info("Initial reconciliation complete", "groups", len(groups))
	return firstErr
}

// checkGroupExpiry checks for and handles expiry of managed instances in a group
func (r *Reconciler) checkGroupExpiry(groupKey string) {
	cfg := r.configLoader.GetCurrent()
	expiry := cfg.Shard.Expiry

	// Skip if no expiry configured
	if expiry.EligibleAge == 0 && expiry.ForcedAge == 0 {
		return
	}

	r.logger.Info("Checking for instance expiry", "group", groupKey)

	// Get current draining state for this group
	currentInstances, err := r.getGroupInstances(groupKey)
	if err != nil {
		r.logger.Error("Failed to get current instances for expiry check", "group", groupKey, "error", err)
		return
	}

	// Check if any instances in this group are currently draining
	drainingCount := 0
	for _, instanceID := range currentInstances {
		instance, err := r.localDB.GetInstance(instanceID)
		if err != nil {
			r.logger.Warn("Failed to get instance details", "instance_id", instanceID, "error", err)
			continue
		}
		if instance.DrainStartedAt != nil {
			drainingCount++
		}
	}

	// Check for forced expiry first (highest priority)
	if expiry.ForcedAge > 0 {
		instances, err := r.localDB.GetInstancesOlderThan(expiry.ForcedAge.Duration(), false) // managed instances only
		if err != nil {
			r.logger.Error("Failed to get instances for forced expiry check", "error", err)
			return
		}

		// Filter to this group only
		for _, instance := range instances {
			if instance.Group == groupKey {
				r.logger.Info("Forced expiry triggered for instance", "instance_id", instance.ID, "age", time.Since(instance.CreatedAt))
				r.expireInstance(instance.ID, "forced-expiry")
				return // Only expire one instance per reconciliation cycle
			}
		}
	}

	// Check for opportunistic expiry if no draining
	if drainingCount == 0 && expiry.EligibleAge > 0 {
		instances, err := r.localDB.GetInstancesOlderThan(expiry.EligibleAge.Duration(), false) // managed instances only
		if err != nil {
			r.logger.Error("Failed to get instances for eligible expiry check", "error", err)
			return
		}

		// Filter to this group only
		for _, instance := range instances {
			if instance.Group == groupKey {
				r.logger.Info("Opportunistic expiry triggered for instance", "instance_id", instance.ID, "age", time.Since(instance.CreatedAt))
				r.expireInstance(instance.ID, "eligible-expiry")
				return // Only expire one instance per reconciliation cycle
			}
		}
	}
}

// checkOnDemandExpiry checks for and handles expiry of on-demand instances
func (r *Reconciler) checkOnDemandExpiry() {
	cfg := r.configLoader.GetCurrent()
	expiry := cfg.Shard.Expiry

	// Skip if no on-demand expiry configured
	if expiry.OndemandAge == 0 {
		return
	}

	r.logger.Info("Checking for on-demand instance expiry")

	instances, err := r.localDB.GetInstancesOlderThan(expiry.OndemandAge.Duration(), true) // on-demand instances only
	if err != nil {
		r.logger.Error("Failed to get on-demand instances for expiry check", "error", err)
		return
	}

	// Expire the oldest on-demand instance
	if len(instances) > 0 {
		oldest := instances[0]
		r.logger.Info("On-demand expiry triggered for instance", "instance_id", oldest.ID, "age", time.Since(oldest.CreatedAt))
		r.expireInstance(oldest.ID, "ondemand-expiry")
		// Note: on-demand instances don't need replacement
	}
}

// expireInstance replaces an expiring instance with a new one, draining the old instance.
func (r *Reconciler) expireInstance(instanceID, reason string) {
	// Get instance details
	instance, err := r.localDB.GetInstance(instanceID)
	if err != nil {
		r.logger.Error("Failed to get instance for expiry", "instance_id", instanceID, "error", err)
		return
	}

	groupKey := instance.Group
	// Get group configuration for drain timeout
	group, err := config.GetGroup(r.ctx, r.configLoader, instance.Tenant, groupKey)
	if err != nil {
		r.logger.Error("Failed to get group configuration for expiry", "tenant", instance.Tenant, "group", groupKey, "error", err)
		return
	}

	r.logger.Info("Expiring instance", "instance_id", instanceID, "group", groupKey, "reason", reason)
	r.replaceAndDrain(instanceID, instance.Tenant, groupKey, reason, *group, false)
}

// scheduleGroupExpiry schedules a timer to trigger expiry check for a group
// when its oldest instance reaches eligibleAge (or forcedAge if sooner)
func (r *Reconciler) scheduleGroupExpiry(tenant, groupKey string) {
	cfg := r.configLoader.GetCurrent()
	expiry := cfg.Shard.Expiry

	if expiry.EligibleAge == 0 && expiry.ForcedAge == 0 {
		return
	}

	oldest, err := r.localDB.GetOldestManagedInstanceByGroup(groupKey)
	if err != nil {
		r.logger.Error("Failed to get oldest instance for expiry scheduling",
			"group", groupKey, "error", err)
		return
	}
	if oldest == nil {
		r.logger.Debug("No eligible instances for expiry scheduling", "group", groupKey)
		r.cancelGroupExpiryTimer(groupKey)
		return
	}

	instanceAge := time.Since(oldest.CreatedAt)

	var nextExpiry time.Duration
	if expiry.EligibleAge > 0 {
		nextExpiry = expiry.EligibleAge.Duration() - instanceAge
	}
	if expiry.ForcedAge > 0 {
		forcedIn := expiry.ForcedAge.Duration() - instanceAge
		if nextExpiry == 0 || forcedIn < nextExpiry {
			nextExpiry = forcedIn
		}
	}

	if nextExpiry <= 0 {
		nextExpiry = expiryTimerPadding
	} else {
		nextExpiry += expiryTimerPadding
	}

	r.expiryTimerMu.Lock()
	defer r.expiryTimerMu.Unlock()

	if existing, ok := r.expiryTimers[groupKey]; ok {
		existing.Stop()
	}

	r.logger.Info("Scheduling expiry check",
		"group", groupKey,
		"instance_id", oldest.ID,
		"instance_age", instanceAge.Round(time.Second).String(),
		"check_in", nextExpiry.Round(time.Second).String())

	r.expiryTimers[groupKey] = time.AfterFunc(nextExpiry, func() {
		r.logger.Info("Expiry timer fired", "tenant", tenant, "group", groupKey)
		r.Enqueue(ReconcileEvent{
			Type:     EventGroupChanged,
			Tenant:   tenant,
			GroupKey: groupKey,
		})
	})
}

// cancelGroupExpiryTimer cancels the expiry timer for a group if one exists
func (r *Reconciler) cancelGroupExpiryTimer(groupKey string) {
	r.expiryTimerMu.Lock()
	defer r.expiryTimerMu.Unlock()

	if timer, ok := r.expiryTimers[groupKey]; ok {
		timer.Stop()
		delete(r.expiryTimers, groupKey)
	}
}

// stopAllExpiryTimers stops all expiry timers (called on shutdown)
func (r *Reconciler) stopAllExpiryTimers() {
	r.expiryTimerMu.Lock()
	defer r.expiryTimerMu.Unlock()

	for groupKey, timer := range r.expiryTimers {
		timer.Stop()
		delete(r.expiryTimers, groupKey)
	}
}

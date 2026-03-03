// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package gc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nstance-dev/nstance/internal/server/infra"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/reconciler"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// Reconciler interface for enqueuing reconciliation events
type Reconciler interface {
	Enqueue(event reconciler.ReconcileEvent)
}

// InstanceGarbageCollector handles garbage collection of dangling provider instances
type InstanceGarbageCollector struct {
	db         *localdb.DB
	provider   infra.Provider
	storage    storage.Storage
	reconciler Reconciler
	logger     *slog.Logger
}

// NewInstanceGarbageCollector creates a new garbage collection service
func NewInstanceGarbageCollector(db *localdb.DB, prov infra.Provider, store storage.Storage, rec Reconciler, logger *slog.Logger) *InstanceGarbageCollector {
	if logger == nil {
		logger = slog.Default()
	}

	return &InstanceGarbageCollector{
		db:         db,
		provider:   prov,
		storage:    store,
		reconciler: rec,
		logger:     logger.With("component", "gc"),
	}
}

// SetReconciler sets the reconciler for the garbage collector.
// This exists because of an init-order dependency: the GC service is created
// before the reconciler, which itself depends on the instance manager.
func (s *InstanceGarbageCollector) SetReconciler(rec Reconciler) {
	s.reconciler = rec
}

// RunGarbageCollection performs a complete garbage collection cycle.
// SQLite is already populated from S3 + provider after leader election via RebuildCache.
// GC does not query the provider during its loop - SQLite has everything needed.
func (s *InstanceGarbageCollector) RunGarbageCollection(ctx context.Context, maxAge time.Duration) error {
	s.logger.Debug("Starting garbage collection cycle", "max_age", maxAge)

	// Terminate instances where the provider reports an unhealthy status
	unhealthy, err := s.db.FindUnhealthyProviderInstances()
	if err != nil {
		return fmt.Errorf("find unhealthy instances: %w", err)
	}
	s.terminateAndCleanup(ctx, "unhealthy", unhealthy)

	// Terminate unregistered instances past timeout
	dangling, err := s.db.FindDanglingInstances(maxAge)
	if err != nil {
		return fmt.Errorf("find dangling instances: %w", err)
	}
	s.terminateAndCleanup(ctx, "dangling", dangling)

	// Clean up stale pre-insert records (provider call never completed)
	stale, err := s.db.FindStalePreInserts(maxAge)
	if err != nil {
		return fmt.Errorf("find stale pre-inserts: %w", err)
	}
	s.terminateAndCleanup(ctx, "stale-pre-insert", stale)

	s.logger.Debug("Completed garbage collection cycle")
	return nil
}

// terminateAndCleanup terminates instances at the provider, removes their S3 records,
// marks them deleted in SQLite, and enqueues reconciliation to replace them.
func (s *InstanceGarbageCollector) terminateAndCleanup(ctx context.Context, reason string, toCleanup []*localdb.Instance) {
	if len(toCleanup) == 0 {
		return
	}

	s.logger.Info("Cleaning up instances", "reason", reason, "count", len(toCleanup))

	var cleanedCount int
	for _, instance := range toCleanup {
		if instance.ProviderID != nil && *instance.ProviderID != "" {
			s.logger.Info("Terminating instance",
				"reason", reason,
				"instance_id", instance.ID,
				"provider_id", *instance.ProviderID)

			if err := s.provider.DeleteInstance(ctx, instance.ID, *instance.ProviderID); err != nil {
				s.logger.Error("Failed to terminate instance",
					"reason", reason,
					"instance_id", instance.ID,
					"provider_id", *instance.ProviderID,
					"error", err)
				continue
			}
		} else {
			s.logger.Info("Cleaning up instance without provider_id",
				"reason", reason,
				"instance_id", instance.ID)
		}

		// Delete S3 record
		storageKey, err := instances.StorageKey(instance.ID)
		if err != nil {
			s.logger.Warn("Failed to generate storage key",
				"instance_id", instance.ID,
				"error", err)
		} else if instance.Tenant == "" {
			s.logger.Error("Instance missing tenant, cannot delete S3 record",
				"instance_id", instance.ID)
		} else {
			key := fmt.Sprintf("instance/%s.%s.json", instance.Tenant, storageKey)
			if err := s.storage.Delete(ctx, key); err != nil {
				s.logger.Warn("Failed to delete S3 record",
					"instance_id", instance.ID,
					"key", key,
					"error", err)
			}
		}

		// Mark deleted in SQLite
		if err := s.db.DeleteInstance(instance.ID); err != nil {
			s.logger.Error("Failed to delete SQLite record",
				"instance_id", instance.ID,
				"error", err)
		}

		// Trigger reconciliation to replace the deleted instance
		if s.reconciler != nil && instance.Tenant != "" && instance.Group != "" {
			s.reconciler.Enqueue(reconciler.ReconcileEvent{
				Type:      reconciler.EventGroupChanged,
				Tenant:    instance.Tenant,
				GroupKey:  instance.Group,
				Timestamp: time.Now().UTC(),
			})
		}

		cleanedCount++
	}

	s.logger.Info("Completed instance cleanup",
		"reason", reason,
		"found", len(toCleanup),
		"cleaned", cleanedCount)
}

// CheckInstanceHealth checks for instances with stale health_at timestamps
func (s *InstanceGarbageCollector) CheckInstanceHealth(ctx context.Context, interval time.Duration) error {
	threshold := time.Now().UTC().Add(-interval)

	instances, err := s.db.QueryStaleHealthInstances(threshold)
	if err != nil {
		return fmt.Errorf("query stale health instances: %w", err)
	}

	for _, instance := range instances {
		if instance.HealthAt == nil {
			continue
		}

		missedIntervals := int(time.Since(*instance.HealthAt) / interval)

		s.logger.Warn("Detected missed health report",
			"instance_id", instance.ID,
			"health_at", instance.HealthAt,
			"missed_intervals", missedIntervals)

		// Enqueue check event with deduplication
		if s.reconciler != nil {
			s.reconciler.Enqueue(reconciler.ReconcileEvent{
				Type:             reconciler.EventCheckInstance,
				InstanceID:       instance.ID,
				Timestamp:        time.Now().UTC(),
				PreventDuplicate: true,
			})
		}
	}

	s.logger.Debug("Health check complete", "stale_instances", len(instances))

	return nil
}

// DefaultDeletedRecordRetention is the default retention period for deleted instance records
const DefaultDeletedRecordRetention = 30 * time.Minute

// CleanupDeletedInstanceRecords removes storage records for instances that have been
// deleted for longer than the retention period.
//
// SAFETY: All code paths that mark instances as deleted are provider-verified:
//  1. Manager.DeleteInstance() - calls provider.DeleteInstance() before marking deleted
//  2. GC.terminateAndCleanup() - terminates via provider before marking deleted
//  3. Manager.seedFromProvider() - marks deleted if provider_id not in provider's instance list
//     (handles EC2 instances terminated while server was down)
//
// We do NOT re-check the provider API here to avoid rate limiting issues.
func (s *InstanceGarbageCollector) CleanupDeletedInstanceRecords(ctx context.Context, retentionPeriod time.Duration) error {
	if retentionPeriod <= 0 {
		retentionPeriod = DefaultDeletedRecordRetention
	}

	cutoff := time.Now().UTC().Add(-retentionPeriod)

	deletedInstances, err := s.db.FindDeletedInstancesPastRetention(cutoff)
	if err != nil {
		return fmt.Errorf("find deleted instances: %w", err)
	}

	if len(deletedInstances) == 0 {
		return nil
	}

	s.logger.Info("Found deleted instances for record cleanup", "count", len(deletedInstances))

	var cleanedCount int

	for _, instance := range deletedInstances {
		storageKey, err := instances.StorageKey(instance.ID)
		if err != nil {
			s.logger.Warn("Failed to generate storage key for deletion",
				"instance_id", instance.ID,
				"error", err)
			continue
		}
		if instance.Tenant == "" {
			s.logger.Error("Instance missing tenant, cannot delete S3 record",
				"instance_id", instance.ID)
			continue
		}
		key := fmt.Sprintf("instance/%s.%s.json", instance.Tenant, storageKey)

		if err := s.storage.Delete(ctx, key); err != nil {
			if err == storage.ErrNotFound {
				s.logger.Debug("Storage record already deleted",
					"instance_id", instance.ID,
					"key", key)
			} else {
				s.logger.Error("Failed to delete storage record",
					"instance_id", instance.ID,
					"key", key,
					"error", err)
				continue
			}
		} else {
			s.logger.Info("Deleted storage record for instance",
				"instance_id", instance.ID,
				"key", key)
		}

		if err := s.db.PurgeDeletedInstance(instance.ID); err != nil {
			s.logger.Error("Failed to purge instance from local DB",
				"instance_id", instance.ID,
				"error", err)
			continue
		}

		cleanedCount++
	}

	s.logger.Info("Completed deleted record cleanup",
		"found", len(deletedInstances),
		"cleaned", cleanedCount)

	return nil
}

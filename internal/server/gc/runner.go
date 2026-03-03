// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package gc

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Runner manages periodic garbage collection runs
type Runner struct {
	service                *InstanceGarbageCollector
	interval               time.Duration // how often to run the GC cycle
	cycleTimeout           time.Duration // context timeout for each GC cycle
	registrationTimeout    time.Duration // max time for instances to register before being considered dangling
	deletedRecordRetention time.Duration // how long to keep storage records after instance deletion
	healthCheckInterval    time.Duration // expected interval between agent health reports
	isLeader               func() bool
	logger                 *slog.Logger
	mu                     sync.Mutex
	wg                     sync.WaitGroup
	cancel                 context.CancelFunc
}

// NewRunner creates a new garbage collector runner
func NewRunner(service *InstanceGarbageCollector, interval, cycleTimeout, registrationTimeout, deletedRecordRetention, healthCheckInterval time.Duration, isLeader func() bool, logger *slog.Logger) *Runner {
	return &Runner{
		service:                service,
		interval:               interval,
		cycleTimeout:           cycleTimeout,
		registrationTimeout:    registrationTimeout,
		deletedRecordRetention: deletedRecordRetention,
		healthCheckInterval:    healthCheckInterval,
		isLeader:               isLeader,
		logger:                 logger,
	}
}

// Start begins the garbage collection loop in a background goroutine
func (r *Runner) Start(ctx context.Context) {
	if r.interval <= 0 {
		r.logger.Info("Garbage collection disabled", "interval", r.interval)
		return
	}

	gcCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	r.wg.Add(1)
	go r.run(gcCtx)
}

// run executes the garbage collection loop
func (r *Runner) run(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	r.runCycle(ctx, "startup")

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("Stopping garbage collection loop")
			return
		case <-ticker.C:
			r.runCycle(ctx, "interval")
		}
	}
}

// runCycle performs a single garbage collection cycle
func (r *Runner) runCycle(ctx context.Context, trigger string) {
	if !r.isLeader() {
		r.logger.Debug("Skipping garbage collection run", "trigger", trigger, "reason", "not shard leader")
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	runCtx := ctx
	var cancel context.CancelFunc
	if r.cycleTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.cycleTimeout)
		defer cancel()
	}

	if err := r.service.RunGarbageCollection(runCtx, r.registrationTimeout); err != nil {
		r.logger.Error("Garbage collection cycle failed", "trigger", trigger, "error", err)
		return
	}

	// Health monitoring
	if err := r.service.CheckInstanceHealth(runCtx, r.healthCheckInterval); err != nil {
		r.logger.Error("Health check failed", "error", err)
	}

	// Cleanup storage records for deleted instances past retention period
	if err := r.service.CleanupDeletedInstanceRecords(runCtx, r.deletedRecordRetention); err != nil {
		r.logger.Error("Deleted record cleanup failed", "error", err)
	}

	r.logger.Info("Garbage collection cycle completed", "trigger", trigger)
}

// Stop terminates the garbage collection loop and waits for completion
func (r *Runner) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

// SetIsLeaderFunc updates the function used to check leadership
func (r *Runner) SetIsLeaderFunc(isLeader func() bool) {
	r.isLeader = isLeader
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

// Package reconciler handles instance lifecycle management: scaling managed groups to desired size,
// detecting and replacing unhealthy instances, coordinating graceful drains, and expiring old instances.
// It processes events from a queue and only acts when the server is the shard leader.
package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/infra"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/localdb"
)

// InstanceManager defines the interface for instance management operations required by the reconciler
type InstanceManager interface {
	CreateInstance(ctx context.Context, req instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error)
	DeleteInstance(ctx context.Context, tenant, instanceID string) error
}

// EventType represents the type of reconciliation event
type EventType string

const (
	EventCheckInstance     EventType = "CheckInstance" // e.g. on gRPC disconnect or missed health report
	EventGroupChanged      EventType = "GroupChanged"
	EventInstanceDeleted   EventType = "InstanceDeleted"
	EventDrainAcked        EventType = "DrainAcked"        // Operator acknowledged drain complete
	EventTerminationNotice EventType = "TerminationNotice" // Instance termination notice received
	EventInitialReconcile  EventType = "InitialReconcile"
)

// ReconcileEvent represents an event that triggers reconciliation
type ReconcileEvent struct {
	Type             EventType
	Tenant           string
	GroupKey         string
	InstanceID       string
	Timestamp        time.Time
	PreventDuplicate bool   // Skip if same event recently processed
	Cause            string // Why this event was triggered (e.g. "disconnect")
	Attempt          int    // Retry attempt number for follow-up polling
}

// groupIdentity identifies a group within a tenant for reconciler state.
type groupIdentity struct {
	tenant string
	group  string
}

// Reconciler handles instance reconciliation (matching actual instance count to desired group size)
type Reconciler struct {
	queue           chan ReconcileEvent
	instanceManager InstanceManager
	configLoader    *config.Loader
	localDB         *localdb.DB
	provider        infra.Provider
	notifyDrain     func(instanceID, group, reason string, unhealthyAt, deleteAt time.Time)
	notifyError     func(tenant, group, instanceID, errMsg string)
	isLeader        func() bool
	logger          *slog.Logger

	// Rate limiting and instance creation serialization
	createRateLimit time.Duration
	lastCreateTime  time.Time
	createMu        sync.Mutex

	// Event deduplication
	recentEvents map[string]time.Time
	eventsMu     sync.RWMutex

	// Expiry timers (one per group)
	expiryTimers  map[groupIdentity]*time.Timer
	expiryTimerMu sync.Mutex

	// Control
	mu      sync.RWMutex
	started bool

	// Shutdown
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Options contains options for creating a Reconciler
type Options struct {
	InstanceManager InstanceManager
	ConfigLoader    *config.Loader
	LocalDB         *localdb.DB
	Provider        infra.Provider
	NotifyDrain     func(instanceID, group, reason string, unhealthyAt, deleteAt time.Time)
	NotifyError     func(tenant, group, instanceID, errMsg string)
	IsLeader        func() bool
	CreateRateLimit time.Duration
	Logger          *slog.Logger
}

// New creates a new Reconciler
func New(opts Options) (*Reconciler, error) {
	if opts.InstanceManager == nil {
		return nil, fmt.Errorf("instance manager is required")
	}
	if opts.ConfigLoader == nil {
		return nil, fmt.Errorf("config loader is required")
	}
	if opts.LocalDB == nil {
		return nil, fmt.Errorf("local database is required")
	}
	if opts.Provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	if opts.NotifyDrain == nil {
		return nil, fmt.Errorf("notifyDrain callback is required")
	}
	if opts.IsLeader == nil {
		return nil, fmt.Errorf("isLeader function is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Reconciler{
		queue:           make(chan ReconcileEvent, 1000),
		instanceManager: opts.InstanceManager,
		configLoader:    opts.ConfigLoader,
		localDB:         opts.LocalDB,
		provider:        opts.Provider,
		notifyDrain:     opts.NotifyDrain,
		notifyError:     opts.NotifyError,
		isLeader:        opts.IsLeader,
		createRateLimit: opts.CreateRateLimit,
		recentEvents:    make(map[string]time.Time),
		expiryTimers:    make(map[groupIdentity]*time.Timer),
		logger:          opts.Logger,
		ctx:             ctx,
		cancel:          cancel,
	}, nil
}

// Start starts the reconciliation loop
func (r *Reconciler) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		r.logger.Debug("Reconciler already started")
		return nil
	}

	r.logger.Info("Starting reconciler")

	// Create new context for this run
	if ctx == nil {
		ctx = context.Background()
	}
	r.ctx, r.cancel = context.WithCancel(ctx)

	// Start reconciliation loop
	r.wg.Add(1)
	go r.reconcileLoop()

	r.started = true
	return nil
}

// Stop stops the reconciliation loop
func (r *Reconciler) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		r.logger.Debug("Reconciler not started")
		return
	}

	r.logger.Info("Stopping reconciler")
	r.stopAllExpiryTimers()
	r.cancel()
	r.wg.Wait()
	r.started = false
	r.logger.Info("Reconciler stopped")
}

// Enqueue adds a reconciliation event to the queue
func (r *Reconciler) Enqueue(event ReconcileEvent) {
	select {
	case r.queue <- event:
		logAttrs := []any{"type", event.Type}
		if event.GroupKey != "" {
			logAttrs = append(logAttrs, "group", event.GroupKey)
		}
		if event.InstanceID != "" {
			logAttrs = append(logAttrs, "instance", event.InstanceID)
		}
		r.logger.Debug("Enqueued reconciliation event", logAttrs...)
	case <-r.ctx.Done():
		r.logger.Warn("Cannot enqueue event, reconciler is stopped")
	default:
		logAttrs := []any{"type", event.Type}
		if event.GroupKey != "" {
			logAttrs = append(logAttrs, "group", event.GroupKey)
		}
		r.logger.Warn("Reconciliation queue full, dropping event", logAttrs...)
	}
}

// reconcileLoop is the main reconciliation loop
func (r *Reconciler) reconcileLoop() {
	defer r.wg.Done()

	for {
		select {
		case <-r.ctx.Done():
			r.logger.Info("Reconciliation loop stopped")
			return
		case event := <-r.queue:
			r.handleEvent(event)
		}
	}
}

// handleEvent processes a reconciliation event
func (r *Reconciler) handleEvent(event ReconcileEvent) {
	// Only process events if we're the shard leader
	if !r.isLeader() {
		r.logger.Debug("Ignoring event, not shard leader",
			"type", event.Type,
			"group", event.GroupKey)
		return
	}

	cfg := r.configLoader.GetCurrent()
	if cfg == nil {
		r.logger.Warn("Ignoring event, config not loaded",
			"type", event.Type,
			"group", event.GroupKey,
			"instance", event.InstanceID)
		return
	}

	// Deduplicate if requested
	var eventKey string
	if event.PreventDuplicate {
		eventKey = fmt.Sprintf("%s:%s:%s", event.Type, event.GroupKey, event.InstanceID)
		cooldown := 2 * cfg.Shard.HealthCheckInterval.Duration()
		now := time.Now().UTC()

		r.eventsMu.Lock()

		// Lazy cleanup: remove stale entries while we have the lock
		if len(r.recentEvents) > 100 {
			cutoff := now.Add(-cooldown)
			for k, v := range r.recentEvents {
				if v.Before(cutoff) {
					delete(r.recentEvents, k)
				}
			}
		}

		// Check for duplicate
		if lastProcessed, exists := r.recentEvents[eventKey]; exists {
			if time.Since(lastProcessed) < cooldown {
				r.logger.Debug("Skipping duplicate event",
					"type", event.Type,
					"instance", event.InstanceID,
					"last_processed", lastProcessed)
				r.eventsMu.Unlock()
				return
			}
		}
		r.eventsMu.Unlock()
	}

	logAttrs := []any{"type", event.Type}
	if event.GroupKey != "" {
		logAttrs = append(logAttrs, "group", event.GroupKey)
	}
	if event.InstanceID != "" {
		logAttrs = append(logAttrs, "instance", event.InstanceID)
	}
	if event.Attempt > 0 {
		logAttrs = append(logAttrs, "attempt", event.Attempt)
	}
	r.logger.Info("Processing reconciliation event", logAttrs...)

	var err error
	switch event.Type {
	case EventInitialReconcile:
		err = r.handleInitialReconcile()
	case EventGroupChanged:
		err = r.handleGroupChanged(event.Tenant, event.GroupKey)
	case EventCheckInstance:
		r.handleCheckInstance(event)
	case EventTerminationNotice:
		err = r.handleSpotTerminating(event.InstanceID)
	case EventInstanceDeleted:
		err = r.handleInstanceDeleted(event.InstanceID, event.Tenant, event.GroupKey)
	case EventDrainAcked:
		err = r.handleDrainAcked(event.Tenant, event.InstanceID)
	default:
		r.logger.Warn("Unknown event type", "type", event.Type)
	}

	// Mark dedup after successful execution (not before, so failures don't suppress retries)
	if err == nil && event.PreventDuplicate {
		r.eventsMu.Lock()
		r.recentEvents[eventKey] = time.Now().UTC()
		r.eventsMu.Unlock()
	}

	// Schedule retry on error
	if err != nil {
		if isPermanentError(err) {
			r.logger.Error("Permanent error processing event, will not retry",
				"type", event.Type,
				"group", event.GroupKey,
				"instance", event.InstanceID,
				"error", err)
			return
		}

		r.scheduleRetry(event, err)
	}
}

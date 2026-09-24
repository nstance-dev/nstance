// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tenantstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tailscale/hujson"

	"github.com/nstance-dev/nstance/internal/server/storage"
)

const (
	stateKey          = "tenants.jsonc"
	maxCASRetries     = 4
	defaultTimerRetry = time.Second
)

// ErrInactive indicates that this server does not own the active shard-leadership term.
var ErrInactive = errors.New("tenant state manager is inactive")

// SleepState is present while a tenant is asleep.
type SleepState struct {
	WakeAt *time.Time `json:"wake_at,omitempty"`
}

// TenantState contains machine-managed runtime state for one tenant.
type TenantState struct {
	Sleep *SleepState `json:"sleep,omitempty"`
}

// stateDocument is the complete persisted tenant runtime-state document.
type stateDocument map[string]TenantState

// Manager persists tenant state with object-storage CAS and resumes wake timers.
type Manager struct {
	storage    storage.Storage
	logger     *slog.Logger
	now        func() time.Time
	timerRetry time.Duration

	mu            sync.RWMutex
	lifecycleMu   sync.Mutex
	documentMu    sync.Mutex
	tenantLocksMu sync.Mutex
	tenantLocks   map[string]chan struct{}
	state         stateDocument
	timers        map[string]*time.Timer
	onChanged     func(string)
	term          *leadershipTerm
}

// leadershipTerm tracks the lifetime and in-flight work of one leadership term.
type leadershipTerm struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a tenant state manager using shard-scoped storage.
func New(shardStorage storage.Storage, logger *slog.Logger) (*Manager, error) {
	if shardStorage == nil {
		return nil, fmt.Errorf("storage is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		storage:     shardStorage,
		logger:      logger,
		now:         func() time.Time { return time.Now().UTC() },
		timerRetry:  defaultTimerRetry,
		tenantLocks: make(map[string]chan struct{}),
		state:       make(stateDocument),
		timers:      make(map[string]*time.Timer),
	}, nil
}

// Start refreshes authoritative state and resumes wake timers.
func (m *Manager) Start(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.stop()
	if ctx == nil {
		ctx = context.Background()
	}
	termCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.term = &leadershipTerm{ctx: termCtx, cancel: cancel}
	m.mu.Unlock()
	if err := m.Refresh(ctx); err != nil {
		m.stop()
		return err
	}
	return nil
}

// Stop cancels wake timers for the current leadership term.
func (m *Manager) Stop() {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.stop()
}

// stop cancels and waits for the current term while lifecycleMu is held.
func (m *Manager) stop() {
	m.mu.Lock()
	term := m.term
	m.term = nil
	if term != nil {
		term.cancel()
	}
	for tenant, timer := range m.timers {
		timer.Stop()
		delete(m.timers, tenant)
	}
	m.mu.Unlock()
	if term != nil {
		term.wg.Wait()
	}
}

// Refresh reloads authoritative tenant state and rebuilds wake timers.
func (m *Manager) Refresh(ctx context.Context) error {
	m.documentMu.Lock()
	defer m.documentMu.Unlock()
	state, _, err := m.load(ctx)
	if err != nil {
		return err
	}
	m.install(state)
	return nil
}

// IsAsleep reports whether the durable sleep entry is present in the refreshed cache.
func (m *Manager) IsAsleep(tenant string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state[tenant].Sleep != nil
}

// SetOnChanged sets the callback invoked after a tenant's durable sleep state changes.
// It must be configured before Start.
func (m *Manager) SetOnChanged(onChanged func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChanged = onChanged
}

// Sleep atomically adds or updates a tenant sleep entry. alreadyAsleep describes
// the state observed before this request; wakeAt is the effective stored deadline.
// check runs while sleep and other tenant operations are excluded.
func (m *Manager) Sleep(ctx context.Context, tenant string, wakeAt *time.Time, check func(context.Context) error) (alreadyAsleep bool, effectiveWakeAt *time.Time, err error) {
	if wakeAt != nil {
		value := wakeAt.UTC()
		wakeAt = &value
	}
	var state stateDocument
	changed := false
	err = m.runForTenant(ctx, tenant, func(ctx context.Context) error {
		if check != nil {
			if err := check(ctx); err != nil {
				return err
			}
		}
		var updateErr error
		state, updateErr = m.update(ctx, func(state stateDocument) bool {
			entry := state[tenant]
			alreadyAsleep = entry.Sleep != nil
			if alreadyAsleep && equalTime(entry.Sleep.WakeAt, wakeAt) {
				effectiveWakeAt = cloneTime(entry.Sleep.WakeAt)
				return false
			}
			entry.Sleep = &SleepState{WakeAt: cloneTime(wakeAt)}
			state[tenant] = entry
			effectiveWakeAt = cloneTime(wakeAt)
			changed = true
			return true
		})
		return updateErr
	})
	if err != nil {
		return false, nil, err
	}
	if changed {
		m.notifyChanged(tenant)
	}
	return alreadyAsleep, cloneTime(state[tenant].Sleep.WakeAt), nil
}

// CreateOnDemand wakes the tenant and creates an on-demand instance.
func (m *Manager) CreateOnDemand(ctx context.Context, tenant string, create func(context.Context) error) error {
	if create == nil {
		return fmt.Errorf("create operation is required")
	}
	return m.runForTenant(ctx, tenant, func(ctx context.Context) error {
		if _, err := m.wake(ctx, tenant); err != nil {
			return err
		}
		return create(ctx)
	})
}

// Wake atomically removes a tenant sleep entry. alreadyAwake describes the state
// observed before this request.
func (m *Manager) Wake(ctx context.Context, tenant string) (alreadyAwake bool, err error) {
	err = m.runForTenant(ctx, tenant, func(ctx context.Context) error {
		alreadyAwake, err = m.wake(ctx, tenant)
		return err
	})
	return alreadyAwake, err
}

// WakeAndWait wakes a tenant and runs wait before another tenant operation may begin.
func (m *Manager) WakeAndWait(ctx context.Context, tenant string, wait func(context.Context) error) (alreadyAwake bool, err error) {
	if wait == nil {
		return false, fmt.Errorf("wait operation is required")
	}
	err = m.runForTenant(ctx, tenant, func(ctx context.Context) error {
		alreadyAwake, err = m.wake(ctx, tenant)
		if err != nil {
			return err
		}
		return wait(ctx)
	})
	return alreadyAwake, err
}

// wake removes a tenant's durable sleep entry while its tenant lock is held.
func (m *Manager) wake(ctx context.Context, tenant string) (alreadyAwake bool, err error) {
	_, err = m.update(ctx, func(state stateDocument) bool {
		entry, exists := state[tenant]
		alreadyAwake = !exists || entry.Sleep == nil
		if alreadyAwake {
			return false
		}
		delete(state, tenant)
		return true
	})
	if err == nil && !alreadyAwake {
		m.notifyChanged(tenant)
	}
	return alreadyAwake, err
}

// update applies a mutation with optimistic concurrency and installs the result.
func (m *Manager) update(ctx context.Context, mutate func(stateDocument) bool) (stateDocument, error) {
	m.documentMu.Lock()
	defer m.documentMu.Unlock()
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state, etag, err := m.load(ctx)
		if err != nil {
			return nil, err
		}
		if !mutate(state) {
			m.install(state)
			return state, nil
		}
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal tenant state: %w", err)
		}
		if err := m.storage.PutIfMatch(ctx, stateKey, data, etag); err != nil {
			if errors.Is(err, storage.ErrPrecondition) {
				continue
			}
			return nil, fmt.Errorf("write tenant state: %w", err)
		}
		m.install(state)
		return state, nil
	}
	return nil, fmt.Errorf("tenant state CAS conflict after %d attempts", maxCASRetries)
}

// load reads and strictly decodes the authoritative state and its validator.
func (m *Manager) load(ctx context.Context) (stateDocument, string, error) {
	data, etag, err := m.storage.Get(ctx, stateKey)
	if errors.Is(err, storage.ErrNotFound) {
		return make(stateDocument), "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("load tenant state: %w", err)
	}
	standard, err := hujson.Standardize(data)
	if err != nil {
		return nil, "", fmt.Errorf("parse tenant state JSONC: %w", err)
	}
	var state stateDocument
	decoder := json.NewDecoder(bytes.NewReader(standard))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, "", fmt.Errorf("decode tenant state: %w", err)
	}
	if state == nil {
		state = make(stateDocument)
	}
	return state, etag, nil
}

// install replaces cached state and rebuilds its wake timers.
func (m *Manager) install(state stateDocument) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
	for tenant, timer := range m.timers {
		timer.Stop()
		delete(m.timers, tenant)
	}
	if m.term == nil || m.term.ctx.Err() != nil {
		return
	}
	for tenant, entry := range state {
		if entry.Sleep != nil && entry.Sleep.WakeAt != nil {
			m.scheduleLocked(tenant, *entry.Sleep.WakeAt)
		}
	}
}

// scheduleLocked schedules a wake while m.mu is held.
func (m *Manager) scheduleLocked(tenant string, wakeAt time.Time) {
	delay := wakeAt.Sub(m.now())
	if delay < 0 {
		delay = 0
	}
	expected := wakeAt
	term := m.term
	m.timers[tenant] = time.AfterFunc(delay, func() { m.wakeIfDue(term, tenant, expected) })
}

// wakeIfDue removes a sleep entry only when its expected deadline is still current.
func (m *Manager) wakeIfDue(term *leadershipTerm, tenant string, expected time.Time) {
	if term == nil || term.ctx.Err() != nil {
		return
	}
	changed := false
	err := m.runForTenant(term.ctx, tenant, func(ctx context.Context) error {
		_, updateErr := m.update(ctx, func(state stateDocument) bool {
			entry, exists := state[tenant]
			if !exists || entry.Sleep == nil || entry.Sleep.WakeAt == nil {
				return false
			}
			if !entry.Sleep.WakeAt.Equal(expected) || entry.Sleep.WakeAt.After(m.now()) {
				return false
			}
			delete(state, tenant)
			changed = true
			return true
		})
		return updateErr
	})
	if err != nil {
		m.logger.Error("Failed timer wake", "tenant", tenant, "error", err)
		if term.ctx.Err() == nil {
			m.mu.Lock()
			if m.term == term && term.ctx.Err() == nil {
				m.timers[tenant] = time.AfterFunc(m.timerRetry, func() { m.wakeIfDue(term, tenant, expected) })
			}
			m.mu.Unlock()
		}
		return
	}
	if changed {
		m.notifyChanged(tenant)
	}
}

// notifyChanged publishes a durable state change without holding manager locks.
func (m *Manager) notifyChanged(tenant string) {
	m.mu.RLock()
	onChanged := m.onChanged
	m.mu.RUnlock()
	if onChanged != nil {
		onChanged(tenant)
	}
}

// runForTenant enters the active leadership term and serializes one tenant's work.
func (m *Manager) runForTenant(ctx context.Context, tenant string, run func(context.Context) error) error {
	ctx, cleanup, err := m.activeContext(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	lock := m.tenantLock(tenant)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lock:
	}
	defer func() { lock <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	err = run(ctx)
	if err == nil {
		err = ctx.Err()
	}
	return err
}

// tenantLock returns the stable semaphore for a tenant.
func (m *Manager) tenantLock(tenant string) chan struct{} {
	m.tenantLocksMu.Lock()
	defer m.tenantLocksMu.Unlock()
	lock := m.tenantLocks[tenant]
	if lock == nil {
		lock = make(chan struct{}, 1)
		lock <- struct{}{}
		m.tenantLocks[tenant] = lock
	}
	return lock
}

// activeContext binds an operation to the active manager term.
func (m *Manager) activeContext(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	term := m.term
	if term == nil || term.ctx.Err() != nil {
		m.mu.Unlock()
		return nil, nil, ErrInactive
	}
	term.wg.Add(1)
	m.mu.Unlock()
	operationCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(term.ctx, cancel)
	return operationCtx, func() {
		stop()
		cancel()
		term.wg.Done()
	}, nil
}

// equalTime compares optional timestamps.
func equalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// cloneTime copies an optional timestamp.
func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

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

	mu         sync.RWMutex
	documentMu sync.Mutex
	state      stateDocument
	timers     map[string]*time.Timer
	ctx        context.Context
	cancel     context.CancelFunc
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
		storage:    shardStorage,
		logger:     logger,
		now:        func() time.Time { return time.Now().UTC() },
		timerRetry: defaultTimerRetry,
		state:      make(stateDocument),
		timers:     make(map[string]*time.Timer),
	}, nil
}

// Start refreshes authoritative state and resumes wake timers.
func (m *Manager) Start(ctx context.Context) error {
	m.Stop()
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.mu.Unlock()
	if err := m.Refresh(ctx); err != nil {
		m.Stop()
		return err
	}
	return nil
}

// Stop cancels wake timers for the current leadership term.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	for tenant, timer := range m.timers {
		timer.Stop()
		delete(m.timers, tenant)
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

// Sleep atomically adds or updates a tenant sleep entry. alreadyAsleep describes
// the state observed before this request; wakeAt is the effective stored deadline.
func (m *Manager) Sleep(ctx context.Context, tenant string, wakeAt *time.Time) (alreadyAsleep bool, effectiveWakeAt *time.Time, err error) {
	ctx, cleanup, err := m.activeContext(ctx)
	if err != nil {
		return false, nil, err
	}
	defer cleanup()

	if wakeAt != nil {
		value := wakeAt.UTC()
		wakeAt = &value
	}
	state, err := m.update(ctx, func(state stateDocument) bool {
		entry := state[tenant]
		alreadyAsleep = entry.Sleep != nil
		if alreadyAsleep && equalTime(entry.Sleep.WakeAt, wakeAt) {
			effectiveWakeAt = cloneTime(entry.Sleep.WakeAt)
			return false
		}
		entry.Sleep = &SleepState{WakeAt: cloneTime(wakeAt)}
		state[tenant] = entry
		effectiveWakeAt = cloneTime(wakeAt)
		return true
	})
	if err != nil {
		return false, nil, err
	}
	return alreadyAsleep, cloneTime(state[tenant].Sleep.WakeAt), nil
}

// Wake atomically removes a tenant sleep entry. alreadyAwake describes the state
// observed before this request.
func (m *Manager) Wake(ctx context.Context, tenant string) (alreadyAwake bool, err error) {
	ctx, cleanup, err := m.activeContext(ctx)
	if err != nil {
		return false, err
	}
	defer cleanup()

	_, err = m.update(ctx, func(state stateDocument) bool {
		entry, exists := state[tenant]
		alreadyAwake = !exists || entry.Sleep == nil
		if alreadyAwake {
			return false
		}
		entry.Sleep = nil
		if entry == (TenantState{}) {
			delete(state, tenant)
		} else {
			state[tenant] = entry
		}
		return true
	})
	if err != nil {
		return false, err
	}
	return alreadyAwake, nil
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
	if m.ctx == nil || m.ctx.Err() != nil {
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
	m.timers[tenant] = time.AfterFunc(delay, func() { m.wakeIfDue(tenant, expected) })
}

// wakeIfDue removes a sleep entry only when its expected deadline is still current.
func (m *Manager) wakeIfDue(tenant string, expected time.Time) {
	m.mu.RLock()
	ctx := m.ctx
	m.mu.RUnlock()
	if ctx == nil || ctx.Err() != nil {
		return
	}
	_, err := m.update(ctx, func(state stateDocument) bool {
		entry, exists := state[tenant]
		if !exists || entry.Sleep == nil || entry.Sleep.WakeAt == nil {
			return false
		}
		if !entry.Sleep.WakeAt.Equal(expected) || entry.Sleep.WakeAt.After(m.now()) {
			return false
		}
		entry.Sleep = nil
		if entry == (TenantState{}) {
			delete(state, tenant)
		} else {
			state[tenant] = entry
		}
		return true
	})
	if err != nil {
		m.logger.Error("Failed timer wake", "tenant", tenant, "error", err)
		if ctx.Err() == nil {
			m.mu.Lock()
			if m.ctx == ctx && ctx.Err() == nil {
				m.timers[tenant] = time.AfterFunc(m.timerRetry, func() { m.wakeIfDue(tenant, expected) })
			}
			m.mu.Unlock()
		}
		return
	}
}

// activeContext binds an operation to the active manager term.
func (m *Manager) activeContext(ctx context.Context) (context.Context, func(), error) {
	m.mu.RLock()
	term := m.ctx
	m.mu.RUnlock()
	if term == nil || term.Err() != nil {
		return nil, nil, ErrInactive
	}
	operationCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(term, cancel)
	return operationCtx, func() {
		stop()
		cancel()
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

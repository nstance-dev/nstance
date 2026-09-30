// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package listeneractivity

import (
	"sync"
	"time"
)

// Observation records the last known activity and current counter availability.
type Observation struct {
	LastActive time.Time
	Available  bool
}

// Tracker keeps listener observations for the lifetime of an nstance-server process.
type Tracker struct {
	mu      sync.RWMutex
	tenants map[string]map[string]map[string]Observation
}

// New returns an empty Tracker.
func New() *Tracker {
	return &Tracker{tenants: make(map[string]map[string]map[string]Observation)}
}

// Update records one listener observation. Recovery from unavailable counters
// starts a new inactivity period.
func (t *Tracker) Update(tenant, listener, instanceID string, active, available bool, observedAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	listeners := t.tenants[tenant]
	if listeners == nil {
		listeners = make(map[string]map[string]Observation)
		t.tenants[tenant] = listeners
	}
	instances := listeners[listener]
	if instances == nil {
		instances = make(map[string]Observation)
		listeners[listener] = instances
	}
	previous, exists := instances[instanceID]
	if active || !available || !exists || !previous.Available {
		previous.LastActive = observedAt.UTC()
	}
	previous.Available = available
	instances[instanceID] = previous
}

// Snapshot returns an independent copy of one tenant's observations.
func (t *Tracker) Snapshot(tenant string) map[string]map[string]Observation {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]map[string]Observation, len(t.tenants[tenant]))
	for listener, instances := range t.tenants[tenant] {
		result[listener] = make(map[string]Observation, len(instances))
		for instanceID, observation := range instances {
			result[listener][instanceID] = observation
		}
	}
	return result
}

// Reset discards a tenant's observations so sleep waits for fresh reports after wake.
func (t *Tracker) Reset(tenant string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tenants, tenant)
}

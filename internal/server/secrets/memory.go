// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"fmt"
	"sync"
)

// Memory implements Store using in-memory storage
type Memory struct {
	mu      sync.RWMutex
	secrets map[string][]byte
}

// NewMemoryStore creates a new in-memory store
func NewMemoryStore() *Memory {
	return &Memory{
		secrets: make(map[string][]byte),
	}
}

// Get retrieves a secret from memory
func (m *Memory) Get(ctx context.Context, name string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, exists := m.secrets[name]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	// Return a copy to prevent external modification
	result := make([]byte, len(data))
	copy(result, data)
	return result, nil
}

// Set stores a secret in memory
func (m *Memory) Set(ctx context.Context, name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Store a copy to prevent external modification
	stored := make([]byte, len(data))
	copy(stored, data)
	m.secrets[name] = stored
	return nil
}

// Delete removes a secret from memory
func (m *Memory) Delete(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.secrets, name)
	return nil
}

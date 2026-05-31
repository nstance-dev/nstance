// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"context"
	"sync"
)

type memoryStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

// newMemoryStore returns an empty in-memory store for tests.
func newMemoryStore() *memoryStore {
	return &memoryStore{data: map[string][]byte{}}
}

// Get returns a copy of the stored value for key or ErrNotFound.
func (s *memoryStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), data...), nil
}

// Put stores a copy of data under key.
func (s *memoryStore) Put(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), data...)
	return nil
}

// Delete removes key from the store or returns ErrNotFound.
func (s *memoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[key]; !ok {
		return ErrNotFound
	}
	delete(s.data, key)
	return nil
}

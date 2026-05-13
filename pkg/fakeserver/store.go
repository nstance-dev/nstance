// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Store implementations when a key does not exist.
var ErrNotFound = errors.New("fakeserver: key not found")

// Store is the persistence interface used by the fake server for its global,
// tenant, and instance state. The fake server owns the key layout beneath this
// interface, so callers only need to provide a store which accepts arbitrary
// keys compatible with ANSI-style relative filenames.
type Store interface {
	// Get returns the value stored under key or ErrNotFound when it is absent.
	Get(ctx context.Context, key string) ([]byte, error)
	// Put stores data under key, replacing any existing value.
	Put(ctx context.Context, key string, data []byte) error
	// Delete removes key or returns ErrNotFound when it is absent.
	Delete(ctx context.Context, key string) error
}

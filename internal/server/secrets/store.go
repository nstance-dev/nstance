// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"errors"
)

// ErrNotFound indicates that a requested secret does not exist.
var ErrNotFound = errors.New("secret not found")

// Store provides simple secret storage operations
type Store interface {
	Get(ctx context.Context, name string) ([]byte, error)
	Set(ctx context.Context, name string, data []byte) error
	Delete(ctx context.Context, name string) error
}

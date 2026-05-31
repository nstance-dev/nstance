// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"log/slog"
	"time"
)

const (
	apiRetryAttempts = 3
	apiRetryBackoff  = 2 * time.Second
)

// retry retries a Proxmox API call up to apiRetryAttempts times with a fixed
// backoff. This handles transient failures such as Proxmox reporting a clone task
// complete before the conf file is fully written.
func retry[T any](ctx context.Context, logger *slog.Logger, op string, fn func() (T, error)) (T, error) {
	var lastErr error
	for attempt := range apiRetryAttempts {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt < apiRetryAttempts-1 {
			logger.Warn("proxmox API call failed, retrying",
				"op", op,
				"attempt", attempt+1,
				"error", err,
			)
			select {
			case <-ctx.Done():
				var zero T
				return zero, ctx.Err()
			case <-time.After(apiRetryBackoff):
			}
		}
	}
	var zero T
	return zero, lastErr
}

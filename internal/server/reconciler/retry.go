// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"errors"
	"math/rand"
	"time"
)

// PermanentError wraps an error to indicate it should not be retried
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// NewPermanentError wraps an error as non-retryable
func NewPermanentError(err error) error {
	return &PermanentError{Err: err}
}

// RetryPolicy defines the retry behavior for a specific event type
type RetryPolicy struct {
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	MaxAttempts int
	JitterFrac  float64 // Fraction of delay to add as jitter (0.25 = 25%)
}

// backoffDelay calculates the delay for a given attempt using exponential backoff with jitter
func backoffDelay(policy RetryPolicy, attempt int) time.Duration {
	delay := policy.BaseDelay * (1 << attempt)
	if delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(float64(delay) * policy.JitterFrac)))
	return delay + jitter
}

// policyFor returns the retry policy for a given event type
func policyFor(eventType EventType) RetryPolicy {
	switch eventType {
	case EventGroupChanged, EventInitialReconcile, EventInstanceDeleted, EventTerminationNotice, EventDrainAcked:
		return RetryPolicy{
			BaseDelay:   5 * time.Second,
			MaxDelay:    2 * time.Minute,
			MaxAttempts: 8,
			JitterFrac:  0.25,
		}
	default:
		return RetryPolicy{
			BaseDelay:   5 * time.Second,
			MaxDelay:    2 * time.Minute,
			MaxAttempts: 8,
			JitterFrac:  0.25,
		}
	}
}

// isPermanentError returns true if the error is wrapped as a PermanentError
func isPermanentError(err error) bool {
	var permErr *PermanentError
	return errors.As(err, &permErr)
}

// scheduleRetry schedules a retry for a failed event with exponential backoff
func (r *Reconciler) scheduleRetry(event ReconcileEvent, err error) {
	policy := policyFor(event.Type)

	if event.Attempt >= policy.MaxAttempts {
		r.logger.Error("Max retry attempts exhausted for event",
			"type", event.Type,
			"group", event.GroupKey,
			"instance", event.InstanceID,
			"attempts", event.Attempt,
			"error", err)
		return
	}

	delay := backoffDelay(policy, event.Attempt)
	r.logger.Warn("Scheduling event retry",
		"type", event.Type,
		"group", event.GroupKey,
		"instance", event.InstanceID,
		"attempt", event.Attempt+1,
		"delay", delay,
		"error", err)

	retryEvent := event
	retryEvent.Attempt = event.Attempt + 1
	retryEvent.PreventDuplicate = false // Retries bypass dedup

	time.AfterFunc(delay, func() {
		r.Enqueue(retryEvent)
	})
}

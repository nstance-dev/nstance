// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package listeneractivity

import (
	"testing"
	"time"
)

// TestTrackerStartsNewIdlePeriodAfterUnavailable verifies unavailable counters
// cannot contribute time toward the inactivity window.
func TestTrackerStartsNewIdlePeriodAfterUnavailable(t *testing.T) {
	tracker := New()
	first := time.Now().UTC().Add(-time.Hour)
	failed := first.Add(10 * time.Minute)
	recovered := failed.Add(10 * time.Minute)
	tracker.Update("red", "public:443", "instance-1", false, true, first)
	tracker.Update("red", "public:443", "instance-1", false, false, failed)
	tracker.Update("red", "public:443", "instance-1", false, true, recovered)

	got := tracker.Snapshot("red")["public:443"]["instance-1"]
	if !got.Available || !got.LastActive.Equal(recovered) {
		t.Fatalf("activity = %#v, want available at %s", got, recovered)
	}
	active := recovered.Add(time.Minute)
	tracker.Update("red", "public:443", "instance-1", true, true, active)
	if got := tracker.Snapshot("red")["public:443"]["instance-1"].LastActive; !got.Equal(active) {
		t.Fatalf("last active = %s, want %s", got, active)
	}
	tracker.Reset("red")
	if got := tracker.Snapshot("red"); len(got) != 0 {
		t.Fatalf("activity after reset = %#v, want empty", got)
	}
}

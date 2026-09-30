// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"strings"
	"testing"
)

// TestCollectEBPFDisabled verifies that an omitted path omits both counters and errors.
func TestCollectEBPFDisabled(t *testing.T) {
	metrics := Metrics{}
	collectEBPF("", &metrics)
	if metrics.EBPFCounters != nil || metrics.EBPFError != nil {
		t.Fatalf("collectEBPF() metrics = %+v, want no eBPF observation", metrics)
	}
}

// TestCollectEBPFMissingLink verifies that configured accounting fails visibly.
func TestCollectEBPFMissingLink(t *testing.T) {
	metrics := Metrics{}
	collectEBPF(t.TempDir(), &metrics)
	if metrics.EBPFError == nil || !strings.Contains(*metrics.EBPFError, "validate pinned eBPF link") {
		t.Fatalf("collectEBPF() error = %v, want pinned-link validation error", metrics.EBPFError)
	}
	if metrics.EBPFCounters != nil {
		t.Fatalf("collectEBPF() counters = %v, want nil", metrics.EBPFCounters)
	}
}

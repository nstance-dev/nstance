// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package health

import (
	"os"
	"testing"
)

// TestCollectEBPFLive verifies the bpffs ABI when given a prepared integration fixture.
func TestCollectEBPFLive(t *testing.T) {
	path := os.Getenv("NSTANCE_TEST_EBPF_COUNTERS_PATH")
	if path == "" {
		t.Skip("NSTANCE_TEST_EBPF_COUNTERS_PATH is not set")
	}
	metrics := Metrics{}
	collectEBPF(path, &metrics)
	if metrics.EBPFError != nil {
		t.Fatalf("collectEBPF() error = %s", *metrics.EBPFError)
	}
	if _, ok := metrics.EBPFCounters[443]; !ok {
		t.Fatalf("collectEBPF() counters = %v, want port 443", metrics.EBPFCounters)
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package health

import "fmt"

// validatePinnedBPFLink reports that pinned BPF objects require Linux.
func validatePinnedBPFLink(string) error {
	return fmt.Errorf("pinned eBPF objects require Linux")
}

// readPinnedEBPFCounters reports that pinned BPF maps require Linux.
func readPinnedEBPFCounters(string) (map[uint32]uint64, error) {
	return nil, fmt.Errorf("pinned eBPF maps require Linux")
}

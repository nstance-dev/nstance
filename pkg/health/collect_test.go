// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMean verifies that CPU core values are averaged for aggregate usage.
func TestMean(t *testing.T) {
	if got := mean([]float64{10, 20, 30}); got != 20 {
		t.Fatalf("mean() = %v, want 20", got)
	}
}

// TestReadUint verifies procfs-style integer parsing.
func TestReadUint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "value")
	if err := os.WriteFile(path, []byte("123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readUint(path); err != nil || got != 123 {
		t.Fatalf("readUint() = %d, %v; want 123, nil", got, err)
	}
}

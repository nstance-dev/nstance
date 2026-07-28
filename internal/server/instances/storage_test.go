// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package instances

import (
	"testing"

	"github.com/puidv7/puidv7-go"
)

func TestStorageKey(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		wantPrefix string
		wantErr    bool
	}{
		{
			name:       "valid instance ID with knc prefix",
			instanceID: mustGeneratePUID(t, "knc"),
			wantPrefix: "knc",
			wantErr:    false,
		},
		{
			name:       "valid instance ID with pod prefix",
			instanceID: mustGeneratePUID(t, "pod"),
			wantPrefix: "pod",
			wantErr:    false,
		},
		{
			name:       "valid instance ID with cls prefix",
			instanceID: mustGeneratePUID(t, "cls"),
			wantPrefix: "cls",
			wantErr:    false,
		},
		{
			name:       "invalid instance ID",
			instanceID: "invalid-id",
			wantErr:    true,
		},
		{
			name:       "empty instance ID",
			instanceID: "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := StorageKey(tt.instanceID)
			if (err != nil) != tt.wantErr {
				t.Errorf("StorageKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if len(got) < 40 {
				t.Errorf("StorageKey() returned key too short: %q", got)
				return
			}

			if got[36] != '-' {
				t.Errorf("StorageKey() missing separator at position 36: %q", got)
				return
			}

			prefix := got[37:]
			if prefix != tt.wantPrefix {
				t.Errorf("StorageKey() prefix = %q, want %q", prefix, tt.wantPrefix)
			}
		})
	}
}

func TestParseStorageKey(t *testing.T) {
	tests := []struct {
		name       string
		storageKey string
		wantErr    bool
	}{
		{
			name:       "too short",
			storageKey: "short",
			wantErr:    true,
		},
		{
			name:       "missing separator",
			storageKey: "01970a1c-e31e-7422-9cd5-e9651d11cc97xknc",
			wantErr:    true,
		},
		{
			name:       "empty prefix",
			storageKey: "01970a1c-e31e-7422-9cd5-e9651d11cc97-",
			wantErr:    true,
		},
		{
			name:       "prefix too long",
			storageKey: "01970a1c-e31e-7422-9cd5-e9651d11cc97-toolong",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseStorageKey(tt.storageKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseStorageKey() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStorageKeyRoundTrip(t *testing.T) {
	prefixes := []string{"knc", "pod", "cls", "vms"}

	for _, prefix := range prefixes {
		t.Run(prefix, func(t *testing.T) {
			originalID := mustGeneratePUID(t, prefix)

			storageKey, err := StorageKey(originalID)
			if err != nil {
				t.Fatalf("StorageKey() error = %v", err)
			}

			recoveredID, err := ParseStorageKey(storageKey)
			if err != nil {
				t.Fatalf("ParseStorageKey() error = %v", err)
			}

			if recoveredID != originalID {
				t.Errorf("Round trip failed: got %q, want %q", recoveredID, originalID)
			}
		})
	}
}

func TestStorageKeyTimeOrdering(t *testing.T) {
	var keys []string

	for i := 0; i < 10; i++ {
		id := mustGeneratePUID(t, "knc")

		key, err := StorageKey(id)
		if err != nil {
			t.Fatalf("StorageKey() error = %v", err)
		}
		keys = append(keys, key)
	}

	for i := 1; i < len(keys); i++ {
		if keys[i] <= keys[i-1] {
			t.Errorf("Storage keys not in time order: keys[%d]=%q <= keys[%d]=%q",
				i, keys[i], i-1, keys[i-1])
		}
	}
}

func mustGeneratePUID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := puidv7.New(prefix)
	if err != nil {
		t.Fatalf("Failed to generate puidv7: %v", err)
	}
	return id
}

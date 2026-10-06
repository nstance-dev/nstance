// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package nat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/v2/internal/server/config"
	"github.com/nstance-dev/nstance/v2/internal/server/storage"
)

// TestAssignmentStoreSharesFixedAddressWithReplacement verifies that fixed
// egress is allocated per instance subnet, not per transient NAT VM.
func TestAssignmentStoreSharesFixedAddressWithReplacement(t *testing.T) {
	ctx := context.Background()
	store, err := NewAssignmentStore(storage.NewMock())
	if err != nil {
		t.Fatal(err)
	}
	addresses := []config.PublicAddress{{IPv4: "192.0.2.1"}, {IPv4: "192.0.2.2"}}
	now := time.Now().UTC()
	one, err := store.Claim(ctx, addresses, "red", "subnet-a", "instance-a", "small", now)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := store.ClaimReplacement(ctx, one, "instance-b", "large", now)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.PublicAddress == nil || replacement.PublicAddress.IPv4 != one.PublicAddress.IPv4 {
		t.Fatalf("replacement address = %#v, want %#v", replacement.PublicAddress, one.PublicAddress)
	}
	if _, err := store.Claim(ctx, addresses, "red", "subnet-b", "instance-c", "small", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, addresses, "red", "subnet-c", "instance-d", "small", now); !errors.Is(err, ErrPublicAddressesExhausted) {
		t.Fatalf("claim error = %v, want fixed address exhaustion", err)
	}
}

// TestAssignmentStoreWithoutFixedAddresses verifies ordinary instance network
// interfaces impose no artificial allocation limit.
func TestAssignmentStoreWithoutFixedAddresses(t *testing.T) {
	ctx := context.Background()
	storage := storage.NewMock()
	store, err := NewAssignmentStore(storage)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		assignment, err := store.Claim(ctx, nil, "red", "subnet-"+id, id, "small", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if assignment.PublicAddress != nil {
			t.Fatalf("assignment %q unexpectedly has fixed egress", id)
		}
	}
	restarted, err := NewAssignmentStore(storage)
	if err != nil {
		t.Fatal(err)
	}
	assignments, err := restarted.Assignments(ctx)
	if err != nil || len(assignments) != 2 {
		t.Fatalf("assignments after restart = %d, %v", len(assignments), err)
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/pkg/proxy"
)

// snapshotReceiver returns a fixed sequence of snapshots.
type snapshotReceiver struct {
	snapshots []*proto.ProxyConfigSnapshot
}

// Recv returns the next configured snapshot.
func (r *snapshotReceiver) Recv() (*proto.ProxyConfigSnapshot, error) {
	if len(r.snapshots) == 0 {
		return nil, io.EOF
	}
	snapshot := r.snapshots[0]
	r.snapshots = r.snapshots[1:]
	return snapshot, nil
}

// TestWatchConfigSnapshotsRejectsInvalidGenerationOrder verifies monotonic generations.
func TestWatchConfigSnapshotsRejectsInvalidGenerationOrder(t *testing.T) {
	tests := []struct {
		name        string
		generations []uint64
		wantError   string
	}{
		{name: "zero after initial publication", generations: []uint64{0, 0}, wantError: "zero after initial"},
		{name: "duplicate", generations: []uint64{4, 4}, wantError: "not greater"},
		{name: "decreasing", generations: []uint64{4, 3}, wantError: "not greater"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver := &snapshotReceiver{}
			for _, generation := range tt.generations {
				receiver.snapshots = append(receiver.snapshots, &proto.ProxyConfigSnapshot{Generation: generation})
			}
			applied := 0
			err := watchConfigSnapshots(receiver, func(proxy.Config) error {
				applied++
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantError)
			}
			if applied != 1 {
				t.Fatalf("applied %d snapshots, want 1", applied)
			}
		})
	}
}

// TestWatchConfigSnapshotsAcceptsInitialZeroAndIncreasingGenerations verifies valid ordering.
func TestWatchConfigSnapshotsAcceptsInitialZeroAndIncreasingGenerations(t *testing.T) {
	receiver := &snapshotReceiver{snapshots: []*proto.ProxyConfigSnapshot{
		{Generation: 0},
		{Generation: 1},
		{Generation: 2},
	}}
	applied := 0
	err := watchConfigSnapshots(receiver, func(proxy.Config) error {
		applied++
		return nil
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want EOF", err)
	}
	if applied != 3 {
		t.Fatalf("applied %d snapshots, want 3", applied)
	}
}

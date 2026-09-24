// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestServeSocketMode verifies the control socket has group-only access.
func TestServeSocketMode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "tunnel.sock")
	service := newTestService(t, &fakeRunner{started: make(chan context.Context, 1)}, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, path, service) }()
	var info os.FileInfo
	var err error
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		info, err = os.Stat(path)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0660 {
		t.Fatalf("socket mode = %04o, want 0660", mode)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tenantstate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/internal/server/storage"
)

// TestManagerSleepUpdateWakeAndCleanup verifies the basic persisted state lifecycle.
func TestManagerSleepUpdateWakeAndCleanup(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMock()
	manager, err := New(store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer manager.Stop()

	firstWake := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	already, effective, err := manager.Sleep(ctx, "red", &firstWake)
	if err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	if already || effective == nil || !effective.Equal(firstWake) {
		t.Fatalf("first sleep = already %v, wake %v", already, effective)
	}
	secondWake := firstWake.Add(time.Hour)
	already, effective, err = manager.Sleep(ctx, "red", &secondWake)
	if err != nil {
		t.Fatalf("update Sleep: %v", err)
	}
	if !already || effective == nil || !effective.Equal(secondWake) {
		t.Fatalf("updated sleep = already %v, wake %v", already, effective)
	}

	alreadyAwake, err := manager.Wake(ctx, "red")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if alreadyAwake {
		t.Fatal("first Wake reported already awake")
	}
	data, _, err := store.Get(ctx, stateKey)
	if err != nil {
		t.Fatalf("Get state: %v", err)
	}
	if string(data) != "{}" {
		t.Fatalf("state after wake = %s, want {}", data)
	}
	alreadyAwake, err = manager.Wake(ctx, "red")
	if err != nil || !alreadyAwake {
		t.Fatalf("idempotent Wake = already %v, err %v", alreadyAwake, err)
	}
}

// conflictOnceStorage injects one concurrent-write conflict.
type conflictOnceStorage struct {
	storage.Storage
	conflicted atomic.Bool
}

// PutIfMatch injects one conflicting update before delegating later writes.
func (s *conflictOnceStorage) PutIfMatch(ctx context.Context, key string, data []byte, etag string) error {
	if !s.conflicted.Swap(true) {
		if err := s.Put(ctx, key, []byte(`{"blue":{"sleep":{}}}`)); err != nil {
			return err
		}
		return storage.ErrPrecondition
	}
	return s.Storage.PutIfMatch(ctx, key, data, etag)
}

// TestManagerRetriesCASConflictWithoutLosingConcurrentState verifies conflict recovery.
func TestManagerRetriesCASConflictWithoutLosingConcurrentState(t *testing.T) {
	ctx := context.Background()
	base := storage.NewMock()
	store := &conflictOnceStorage{Storage: base}
	manager, err := New(store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer manager.Stop()
	if _, _, err := manager.Sleep(ctx, "red", nil); err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	data, _, err := base.Get(ctx, stateKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var state stateDocument
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if state["red"].Sleep == nil || state["blue"].Sleep == nil {
		t.Fatalf("state after conflict = %#v", state)
	}
}

// TestManagerRestartResumesTimerAndConcurrentWakeConverges verifies restart recovery.
func TestManagerRestartResumesTimerAndConcurrentWakeConverges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := storage.NewMock()
	first, err := New(store, nil)
	if err != nil {
		t.Fatalf("New first manager: %v", err)
	}
	if err := first.Start(ctx); err != nil {
		t.Fatalf("Start first manager: %v", err)
	}
	wakeAt := time.Now().UTC().Add(80 * time.Millisecond)
	if _, _, err := first.Sleep(ctx, "red", &wakeAt); err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	first.Stop()

	restarted, err := New(store, nil)
	if err != nil {
		t.Fatalf("New restarted manager: %v", err)
	}
	if err := restarted.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer restarted.Stop()
	deadline := time.Now().Add(time.Second)
	for restarted.IsAsleep("red") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if restarted.IsAsleep("red") {
		t.Fatal("tenant remains asleep after timer")
	}

	if _, _, err := restarted.Sleep(ctx, "red", nil); err != nil {
		t.Fatalf("second Sleep: %v", err)
	}
	var woke atomic.Int32
	var already atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wasAwake, err := restarted.Wake(ctx, "red")
			if err != nil {
				t.Errorf("Wake: %v", err)
				return
			}
			if wasAwake {
				already.Add(1)
			} else {
				woke.Add(1)
			}
		}()
	}
	wg.Wait()
	if woke.Load() != 1 || already.Load() != 15 {
		t.Fatalf("wake results = %d woke, %d already awake", woke.Load(), already.Load())
	}
}

// TestManagerRejectsUnknownStateFields verifies strict persisted-state decoding.
func TestManagerRejectsUnknownStateFields(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMock()
	if err := store.Put(ctx, stateKey, []byte(`{"red":{"unknown":true}}`)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	manager, err := New(store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = manager.Refresh(ctx)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Refresh error = %v, want unknown-field error", err)
	}
}

// TestManagerRejectsTransitionAfterStop verifies inactive managers reject writes.
func TestManagerRejectsTransitionAfterStop(t *testing.T) {
	ctx := context.Background()
	manager, err := New(storage.NewMock(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	manager.Stop()
	if _, _, err := manager.Sleep(ctx, "red", nil); !errors.Is(err, ErrInactive) {
		t.Fatalf("Sleep after Stop error = %v, want ErrInactive", err)
	}
	if _, err := manager.Wake(ctx, "red"); !errors.Is(err, ErrInactive) {
		t.Fatalf("Wake after Stop error = %v, want ErrInactive", err)
	}
}

// failPutStorage injects a transient conditional-write failure.
type failPutStorage struct {
	storage.Storage
	fail atomic.Bool
}

// PutIfMatch fails once when requested, then delegates later writes.
func (s *failPutStorage) PutIfMatch(ctx context.Context, key string, data []byte, etag string) error {
	if s.fail.Swap(false) {
		return errors.New("temporary write failure")
	}
	return s.Storage.PutIfMatch(ctx, key, data, etag)
}

// TestManagerTimerRetriesTransientFailure verifies a failed timed wake is retried.
func TestManagerTimerRetriesTransientFailure(t *testing.T) {
	ctx := context.Background()
	store := &failPutStorage{Storage: storage.NewMock()}
	manager, err := New(store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	manager.timerRetry = 10 * time.Millisecond
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer manager.Stop()
	wakeAt := time.Now().UTC().Add(30 * time.Millisecond)
	if _, _, err := manager.Sleep(ctx, "red", &wakeAt); err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	store.fail.Store(true)
	deadline := time.Now().Add(time.Second)
	for manager.IsAsleep("red") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if manager.IsAsleep("red") {
		t.Fatal("tenant remains asleep after timer write retry")
	}
}

// TestManagerStaleTimerDoesNotRemoveUpdatedSleep verifies old timers cannot remove new state.
func TestManagerStaleTimerDoesNotRemoveUpdatedSleep(t *testing.T) {
	ctx := context.Background()
	manager, err := New(storage.NewMock(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer manager.Stop()
	first := time.Now().UTC().Add(30 * time.Millisecond)
	if _, _, err := manager.Sleep(ctx, "red", &first); err != nil {
		t.Fatalf("first Sleep: %v", err)
	}
	updated := time.Now().UTC().Add(200 * time.Millisecond)
	if _, _, err := manager.Sleep(ctx, "red", &updated); err != nil {
		t.Fatalf("updated Sleep: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if !manager.IsAsleep("red") {
		t.Fatal("stale timer removed updated sleep entry")
	}
}

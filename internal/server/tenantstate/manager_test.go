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
	already, effective, err := manager.Sleep(ctx, "red", &firstWake, nil)
	if err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	if already || effective == nil || !effective.Equal(firstWake) {
		t.Fatalf("first sleep = already %v, wake %v", already, effective)
	}
	secondWake := firstWake.Add(time.Hour)
	already, effective, err = manager.Sleep(ctx, "red", &secondWake, nil)
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
	if _, _, err := manager.Sleep(ctx, "red", nil, nil); err != nil {
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
	if _, _, err := first.Sleep(ctx, "red", &wakeAt, nil); err != nil {
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

	if _, _, err := restarted.Sleep(ctx, "red", nil, nil); err != nil {
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

// TestManagerRejectsOperationAfterStop verifies inactive managers reject writes.
func TestManagerRejectsOperationAfterStop(t *testing.T) {
	ctx := context.Background()
	manager, err := New(storage.NewMock(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	manager.Stop()
	if _, _, err := manager.Sleep(ctx, "red", nil, nil); !errors.Is(err, ErrInactive) {
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
	if _, _, err := manager.Sleep(ctx, "red", &wakeAt, nil); err != nil {
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
	if _, _, err := manager.Sleep(ctx, "red", &first, nil); err != nil {
		t.Fatalf("first Sleep: %v", err)
	}
	updated := time.Now().UTC().Add(200 * time.Millisecond)
	if _, _, err := manager.Sleep(ctx, "red", &updated, nil); err != nil {
		t.Fatalf("updated Sleep: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if !manager.IsAsleep("red") {
		t.Fatal("stale timer removed updated sleep entry")
	}
}

// TestManagerGuardsSleepAndOnDemandCreation verifies domain checks run under tenant serialization.
func TestManagerGuardsSleepAndOnDemandCreation(t *testing.T) {
	ctx := context.Background()
	manager, err := New(storage.NewMock(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer manager.Stop()

	blocked := errors.New("blocked")
	if _, _, err := manager.Sleep(ctx, "red", nil, func(context.Context) error { return blocked }); !errors.Is(err, blocked) {
		t.Fatalf("Sleep check error = %v, want %v", err, blocked)
	}
	if manager.IsAsleep("red") {
		t.Fatal("failed sleep check persisted sleep state")
	}
	if _, _, err := manager.Sleep(ctx, "red", nil, nil); err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	created := false
	awakeAtCreate := false
	err = manager.CreateOnDemand(ctx, "red", func(context.Context) error {
		created = true
		awakeAtCreate = !manager.IsAsleep("red")
		return nil
	})
	if err != nil || !created || !awakeAtCreate || manager.IsAsleep("red") {
		t.Fatalf("CreateOnDemand = created %v, awake at create %v, asleep afterward %v, error %v", created, awakeAtCreate, manager.IsAsleep("red"), err)
	}
}

// TestManagerSerializesTenantOperations verifies every operation shares one tenant lock.
func TestManagerSerializesTenantOperations(t *testing.T) {
	ctx := context.Background()
	manager, err := New(storage.NewMock(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer manager.Stop()
	if _, _, err := manager.Sleep(ctx, "red", nil, nil); err != nil {
		t.Fatalf("Sleep: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	operationDone := make(chan error, 1)
	go func() {
		_, err := manager.WakeAndWait(ctx, "red", func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
		operationDone <- err
	}()
	<-entered

	sleepDone := make(chan error, 1)
	go func() {
		_, _, err := manager.Sleep(ctx, "red", nil, nil)
		sleepDone <- err
	}()
	select {
	case err := <-sleepDone:
		t.Fatalf("Sleep completed while wake was waiting: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	otherDone := make(chan error, 1)
	go func() {
		_, _, err := manager.Sleep(ctx, "blue", nil, nil)
		otherDone <- err
	}()
	select {
	case err := <-otherDone:
		if err != nil {
			t.Fatalf("other tenant operation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("independent tenant operation was blocked")
	}

	close(release)
	if err := <-operationDone; err != nil {
		t.Fatalf("WakeAndWait: %v", err)
	}
	if err := <-sleepDone; err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	if !manager.IsAsleep("red") {
		t.Fatal("sleep did not run after wake completed")
	}
}

// TestManagerLeadershipLossCancelsWaitingOperation verifies old-term work cannot outlive leadership.
func TestManagerLeadershipLossCancelsWaitingOperation(t *testing.T) {
	ctx := context.Background()
	manager, err := New(storage.NewMock(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = manager.runForTenant(ctx, "red", func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- manager.runForTenant(ctx, "red", func(context.Context) error { return nil })
	}()
	stopDone := make(chan struct{})
	go func() {
		manager.Stop()
		close(stopDone)
	}()
	if err := <-waiterDone; !errors.Is(err, context.Canceled) && !errors.Is(err, ErrInactive) {
		t.Fatalf("waiting operation error = %v, want context cancellation or ErrInactive", err)
	}
	close(release)
	<-stopDone
}

// TestManagerNewTermWaitsForOldOperation verifies terms cannot overlap tenant work.
func TestManagerNewTermWaitsForOldOperation(t *testing.T) {
	manager, err := New(storage.NewMock(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start first term: %v", err)
	}
	enteredOld := make(chan struct{})
	releaseOld := make(chan struct{})
	oldDone := make(chan error, 1)
	go func() {
		oldDone <- manager.runForTenant(context.Background(), "red", func(context.Context) error {
			close(enteredOld)
			<-releaseOld
			return nil
		})
	}()
	<-enteredOld
	restarted := make(chan error, 1)
	go func() { restarted <- manager.Start(context.Background()) }()
	defer manager.Stop()
	select {
	case err := <-restarted:
		t.Fatalf("new term started before old operation exited: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseOld)
	if err := <-oldDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("old operation error = %v, want context cancellation", err)
	}
	if err := <-restarted; err != nil {
		t.Fatalf("Start second term: %v", err)
	}
	enteredNew := make(chan struct{})
	newDone := make(chan error, 1)
	go func() {
		newDone <- manager.runForTenant(context.Background(), "blue", func(context.Context) error {
			close(enteredNew)
			return nil
		})
	}()
	select {
	case <-enteredNew:
	case <-time.After(time.Second):
		t.Fatal("new operation did not start after old operation exited")
	}
	if err := <-newDone; err != nil {
		t.Fatalf("new operation: %v", err)
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package gc

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/internal/server/infra"
	"github.com/nstance-dev/nstance/internal/server/infra/mock"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
	"github.com/puidv7/puidv7-go"
)

func TestInstanceGarbageCollector(t *testing.T) {
	// Create temporary database
	tmpDB := "/tmp/test_gc.db"
	t.Cleanup(func() {
		err := os.Remove(tmpDB)
		if err != nil {
			t.Fatalf("Failed to remove temp database file: %v", err)
		}
	})

	// Setup database
	db, err := localdb.Open(tmpDB)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	t.Cleanup(func() {
		err := db.Close()
		if err != nil {
			t.Fatalf("Failed to close database: %v", err)
		}
	})

	// Setup mock provider
	mockProvider := mock.NewProvider(mock.Options{
		Config: infra.ProviderConfig{
			Kind:   "mock",
			Region: "us-east-1",
			Zone:   "us-east-1a",
		},
	})

	// Setup mock storage
	mockStorage := storage.NewMock()

	// Create GC service
	gcService := NewInstanceGarbageCollector(db, mockProvider, mockStorage, nil, nil)

	// Test 1: Find dangling instances
	t.Run("FindDanglingInstances", func(t *testing.T) {
		danglingInstances, err := db.FindDanglingInstances(24 * time.Hour)
		if err != nil {
			t.Fatalf("Failed to find dangling instances: %v", err)
		}

		if len(danglingInstances) != 0 {
			t.Errorf("Expected 0 dangling instances in empty DB, got %d", len(danglingInstances))
		}
	})

	// Test 2: Garbage collection with age restriction
	t.Run("GarbageCollectionWithAge", func(t *testing.T) {
		// Reset and clear existing state
		mockProvider.Reset()
		instances, _ := db.ListInstances()
		for _, inst := range instances {
			_ = db.DeleteInstance(inst.ID)
		}

		// Create instances in both mock provider and DB with issued_at in the past
		oldTime := time.Now().UTC().Add(-1 * time.Hour)
		for i := 0; i < 3; i++ {
			instanceID := fmt.Sprintf("gc-test-instance-%d", i)
			// First create in provider
			req := infra.CreateInstanceRequest{
				ClusterID:    "cls_test123",
				Shard:        "us-east-1a",
				Group:        "test-group",
				InstanceID:   instanceID,
				InstanceType: "t3.micro",
				SubnetID:     "subnet-12345",
				Args:         map[string]interface{}{"ImageId": "ami-12345"},
			}
			providerResp, err := mockProvider.CreateInstance(context.Background(), req)
			if err != nil {
				t.Fatalf("Failed to create mock instance: %v", err)
			}

			// Then create in DB with old issued_at
			inst := &localdb.Instance{
				ID:         instanceID,
				Tenant:     "default",
				Group:      "test-group",
				ProviderID: &providerResp.ProviderInstanceID,
				Nonce:      fmt.Sprintf("test-nonce-%d", i),
				IssuedAt:   &oldTime,
				CreatedAt:  oldTime,
			}
			if err := db.CreateInstance(inst); err != nil {
				t.Fatalf("Failed to create test instance in DB: %v", err)
			}
		}

		// Wait for mock provider to mark instances as running
		time.Sleep(150 * time.Millisecond)

		// Run GC with very short max age (should terminate all instances with old issued_at)
		err := gcService.RunGarbageCollection(context.Background(), 1*time.Millisecond)
		if err != nil {
			t.Fatalf("Failed to run garbage collection: %v", err)
		}

		// Wait for termination to complete
		time.Sleep(100 * time.Millisecond)

		// Verify instances were marked as deleted
		instances, err = db.ListInstances()
		if err != nil {
			t.Fatalf("Failed to list instances: %v", err)
		}

		// All instances should be marked as deleted (empty list from ListInstances)
		if len(instances) != 0 {
			t.Errorf("Expected 0 active instances after GC, got %d", len(instances))
		}
	})

	// Test 3: Provider pagination
	t.Run("Pagination", func(t *testing.T) {
		mockProvider.Reset()

		// Create many instances with semantic fields
		for i := 0; i < 150; i++ {
			req := infra.CreateInstanceRequest{
				ClusterID:    "cls_test123",
				Shard:        "us-east-1a",
				Group:        "test-group",
				InstanceID:   "page-test-" + string(rune('A'+i%26)) + string(rune('0'+i/26)),
				InstanceType: "t3.micro",
				SubnetID:     "subnet-12345",
				Args: map[string]interface{}{
					"ImageId": "ami-12345",
				},
			}
			_, err := mockProvider.CreateInstance(context.Background(), req)
			if err != nil {
				t.Fatalf("Failed to create mock instance: %v", err)
			}
		}

		// Test pagination
		req := infra.ListInstancesRequest{
			ClusterID: "cls_test123",
			Shard:     "us-east-1a",
			Limit:     50,
		}

		resp, err := mockProvider.ListInstances(context.Background(), req)
		if err != nil {
			t.Fatalf("Failed to list with pagination: %v", err)
		}

		if len(resp.Instances) != 50 {
			t.Errorf("Expected 50 instances in first page, got %d", len(resp.Instances))
		}

		if resp.NextToken == "" {
			t.Error("Expected next token for pagination")
		}
	})
}

func TestCleanupDeletedInstanceRecords(t *testing.T) {
	// Create temporary database
	tmpDB := "/tmp/test_gc_record_cleanup.db"
	t.Cleanup(func() {
		_ = os.Remove(tmpDB)
	})

	// Setup database
	db, err := localdb.Open(tmpDB)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	// Setup mock provider
	mockProvider := mock.NewProvider(mock.Options{
		Config: infra.ProviderConfig{
			Kind:   "mock",
			Region: "us-east-1",
			Zone:   "us-east-1a",
		},
	})

	// Setup mock storage
	mockStorage := storage.NewMock()

	// Create GC service
	gcService := NewInstanceGarbageCollector(db, mockProvider, mockStorage, nil, nil)

	ctx := context.Background()

	t.Run("CleanupDeletedInstancesAfterRetention", func(t *testing.T) {
		// Generate a valid puidv7 ID
		instanceID, err := puidv7.New("knc")
		if err != nil {
			t.Fatalf("Failed to generate puidv7: %v", err)
		}
		instanceStorageKey, err := instances.StorageKey(instanceID)
		if err != nil {
			t.Fatalf("Failed to generate storage key: %v", err)
		}

		// Create an instance in DB
		now := time.Now().UTC()
		deletedAt := now.Add(-1 * time.Hour) // Deleted 1 hour ago
		instance := &localdb.Instance{
			ID:        instanceID,
			Tenant:    "default",
			Group:     "test-group",
			Nonce:     "test-nonce-1",
			CreatedAt: now.Add(-2 * time.Hour),
			DeletedAt: &deletedAt,
		}
		if err := db.CreateInstance(instance); err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		// Create corresponding storage record using tenant-prefixed key format
		// S3 path format: instance/{tenant}.{storage-key}.json (within shard scope)
		storageKey := "instance/default." + instanceStorageKey + ".json"
		if err := mockStorage.Put(ctx, storageKey, []byte(`{"instanceId":"`+instanceID+`"}`)); err != nil {
			t.Fatalf("Failed to create storage record: %v", err)
		}

		// Verify storage record exists
		exists, err := mockStorage.Exists(ctx, storageKey)
		if err != nil {
			t.Fatalf("Failed to check storage record existence: %v", err)
		}
		if !exists {
			t.Fatal("Storage record should exist before cleanup")
		}

		// Run cleanup with 30 minute retention (instance was deleted 1 hour ago)
		if err := gcService.CleanupDeletedInstanceRecords(ctx, 30*time.Minute); err != nil {
			t.Fatalf("Failed to cleanup records: %v", err)
		}

		// Verify storage record was deleted
		exists, err = mockStorage.Exists(ctx, storageKey)
		if err != nil {
			t.Fatalf("Failed to check storage record after cleanup: %v", err)
		}
		if exists {
			t.Error("Storage record should have been deleted after cleanup")
		}

		// Verify instance was purged from DB
		_, err = db.GetInstance(instanceID)
		if err == nil {
			t.Error("Instance should have been purged from DB")
		}
	})

	t.Run("SkipRecentlyDeletedInstances", func(t *testing.T) {
		// Generate a valid puidv7 ID
		instanceID, err := puidv7.New("knc")
		if err != nil {
			t.Fatalf("Failed to generate puidv7: %v", err)
		}
		instanceStorageKey, err := instances.StorageKey(instanceID)
		if err != nil {
			t.Fatalf("Failed to generate storage key: %v", err)
		}

		// Create an instance that was deleted recently
		now := time.Now().UTC()
		deletedAt := now.Add(-5 * time.Minute) // Deleted 5 minutes ago
		instance := &localdb.Instance{
			ID:        instanceID,
			Tenant:    "default",
			Group:     "test-group",
			Nonce:     "test-nonce-2",
			CreatedAt: now.Add(-1 * time.Hour),
			DeletedAt: &deletedAt,
		}
		if err := db.CreateInstance(instance); err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		// Create corresponding storage record using tenant-prefixed key format
		// S3 path format: instance/{tenant}.{storage-key}.json (within shard scope)
		storageKey := "instance/default." + instanceStorageKey + ".json"
		if err := mockStorage.Put(ctx, storageKey, []byte(`{"instanceId":"`+instanceID+`"}`)); err != nil {
			t.Fatalf("Failed to create storage record: %v", err)
		}

		// Run cleanup with 30 minute retention (instance was deleted 5 minutes ago, should be skipped)
		if err := gcService.CleanupDeletedInstanceRecords(ctx, 30*time.Minute); err != nil {
			t.Fatalf("Failed to cleanup records: %v", err)
		}

		// Verify storage record still exists (not cleaned up yet)
		exists, err := mockStorage.Exists(ctx, storageKey)
		if err != nil {
			t.Fatalf("Failed to check storage record after cleanup: %v", err)
		}
		if !exists {
			t.Error("Storage record should NOT have been deleted (within retention period)")
		}
	})

	t.Run("HandleMissingStorageRecord", func(t *testing.T) {
		// Generate a valid puidv7 ID
		instanceID, err := puidv7.New("knc")
		if err != nil {
			t.Fatalf("Failed to generate puidv7: %v", err)
		}

		// Create an instance that was deleted but has no storage record
		now := time.Now().UTC()
		deletedAt := now.Add(-1 * time.Hour)
		instance := &localdb.Instance{
			ID:        instanceID,
			Tenant:    "default",
			Group:     "test-group",
			Nonce:     "test-nonce-3",
			CreatedAt: now.Add(-2 * time.Hour),
			DeletedAt: &deletedAt,
		}
		if err := db.CreateInstance(instance); err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		// Run cleanup - should handle missing storage record gracefully
		if err := gcService.CleanupDeletedInstanceRecords(ctx, 30*time.Minute); err != nil {
			t.Fatalf("Failed to cleanup records: %v", err)
		}

		// Verify instance was still purged from DB
		_, err = db.GetInstance(instanceID)
		if err == nil {
			t.Error("Instance should have been purged from DB even without storage record")
		}
	})

	t.Run("UseDefaultRetentionPeriod", func(t *testing.T) {
		// Generate a valid puidv7 ID
		instanceID, err := puidv7.New("knc")
		if err != nil {
			t.Fatalf("Failed to generate puidv7: %v", err)
		}
		instanceStorageKey, err := instances.StorageKey(instanceID)
		if err != nil {
			t.Fatalf("Failed to generate storage key: %v", err)
		}

		// Create an instance that was deleted 40 minutes ago (past default 30m retention)
		now := time.Now().UTC()
		deletedAt := now.Add(-40 * time.Minute)
		instance := &localdb.Instance{
			ID:        instanceID,
			Tenant:    "default",
			Group:     "test-group",
			Nonce:     "test-nonce-4",
			CreatedAt: now.Add(-2 * time.Hour),
			DeletedAt: &deletedAt,
		}
		if err := db.CreateInstance(instance); err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		// Create corresponding storage record using tenant-prefixed key format
		// S3 path format: instance/{tenant}.{storage-key}.json (within shard scope)
		storageKey := "instance/default." + instanceStorageKey + ".json"
		if err := mockStorage.Put(ctx, storageKey, []byte(`{"instanceId":"`+instanceID+`"}`)); err != nil {
			t.Fatalf("Failed to create storage record: %v", err)
		}

		// Run cleanup with 0 duration (should use default 30m)
		if err := gcService.CleanupDeletedInstanceRecords(ctx, 0); err != nil {
			t.Fatalf("Failed to cleanup records: %v", err)
		}

		// Verify storage record was deleted (40 minutes > 30 minute default)
		exists, err := mockStorage.Exists(ctx, storageKey)
		if err != nil {
			t.Fatalf("Failed to check storage record after cleanup: %v", err)
		}
		if exists {
			t.Error("Storage record should have been deleted with default retention period")
		}
	})
}

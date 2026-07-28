// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package localdb

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/puidv7/puidv7-go"
)

// Helper function for creating time pointers
func timePtr(t time.Time) *time.Time {
	return &t
}

// TestGroupInstanceQueriesAreTenantScoped verifies same-key groups remain tenant-isolated.
func TestGroupInstanceQueriesAreTenantScoped(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	running := []byte(`{"status":"running"}`)
	instances := []*Instance{
		{ID: "red-old", Tenant: "red", Group: "workers", Nonce: "red-old", ProviderID: stringPtr("provider-red-old"), ProviderState: running, CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "red-new", Tenant: "red", Group: "workers", Nonce: "red-new", ProviderID: stringPtr("provider-red-new"), ProviderState: running, CreatedAt: now.Add(-time.Hour)},
		{ID: "blue-old", Tenant: "blue", Group: "workers", Nonce: "blue-old", ProviderID: stringPtr("provider-blue-old"), ProviderState: running, CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "on-demand", Tenant: "red", Group: "adhoc", Nonce: "on-demand", OnDemand: true, ProviderState: running, CreatedAt: now},
		{ID: "deleting", Tenant: "red", Group: "old", Nonce: "deleting", ProviderState: []byte(`{"status":"deleting"}`), CreatedAt: now},
	}
	for _, instance := range instances {
		if err := db.CreateInstance(instance); err != nil {
			t.Fatalf("create %s: %v", instance.ID, err)
		}
	}

	redIDs, err := db.GetInstancesByGroup("red", "workers", true)
	if err != nil || fmt.Sprint(redIDs) != "[red-old red-new]" {
		t.Fatalf("red instances = %v, err = %v", redIDs, err)
	}
	blueCount, err := db.GetInstanceCountByGroup("blue", "workers", true)
	if err != nil || blueCount != 1 {
		t.Fatalf("blue count = %d, err = %v", blueCount, err)
	}
	oldest, err := db.GetOldestManagedInstanceByGroup("red", "workers")
	if err != nil || oldest == nil || oldest.ID != "red-old" {
		t.Fatalf("red oldest = %#v, err = %v", oldest, err)
	}
	providerIDs, err := db.GetProviderIDsByGroup("blue", "workers", true)
	if err != nil || fmt.Sprint(providerIDs) != "[provider-blue-old]" {
		t.Fatalf("blue provider IDs = %v, err = %v", providerIDs, err)
	}
	identities, err := db.GetActiveManagedGroupIdentities()
	if err != nil || fmt.Sprint(identities) != "[{blue workers} {red workers}]" {
		t.Fatalf("active managed group identities = %v, err = %v", identities, err)
	}
}

// stringPtr returns a pointer to value for test records.
func stringPtr(value string) *string {
	return &value
}

func TestDatabase(t *testing.T) {
	// Create temporary database file
	tempFile := "/tmp/nstance-test.db"
	defer func() { _ = os.Remove(tempFile) }()

	db, err := Open(tempFile)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	t.Run("ImageOperations", func(t *testing.T) {
		now := time.Now().UTC()

		// Test UpsertImages
		err := db.UpsertImages(map[string]string{"test-image": "ami-12345"}, now)
		if err != nil {
			t.Fatalf("Failed to upsert image: %v", err)
		}

		// Test GetImage
		imageID, resolvedAt, err := db.GetImage("test-image")
		if err != nil {
			t.Fatalf("Failed to get image: %v", err)
		}
		if imageID != "ami-12345" {
			t.Errorf("Expected image ID 'ami-12345', got '%s'", imageID)
		}
		if resolvedAt.Unix() != now.Unix() {
			t.Errorf("ResolvedAt mismatch: expected %v, got %v", now, resolvedAt)
		}

		// Test UpsertImages
		images := map[string]string{
			"ubuntu-lts": "ami-ubuntu",
			"centos":     "ami-centos",
		}
		now2 := time.Now().UTC()
		err = db.UpsertImages(images, now2)
		if err != nil {
			t.Fatalf("Failed to upsert images: %v", err)
		}

		// Test GetImages
		allImages, err := db.GetImages()
		if err != nil {
			t.Fatalf("Failed to get images: %v", err)
		}

		// Should have 3 images now (test-image, ubuntu-lts, centos)
		if len(allImages) != 3 {
			t.Errorf("Expected 3 images, got %d", len(allImages))
		}
		if allImages["test-image"] != "ami-12345" {
			t.Errorf("Expected test-image to be 'ami-12345', got '%s'", allImages["test-image"])
		}
		if allImages["ubuntu-lts"] != "ami-ubuntu" {
			t.Errorf("Expected ubuntu-lts to be 'ami-ubuntu', got '%s'", allImages["ubuntu-lts"])
		}
		if allImages["centos"] != "ami-centos" {
			t.Errorf("Expected centos to be 'ami-centos', got '%s'", allImages["centos"])
		}

		// Test updating existing image
		now3 := time.Now().UTC()
		err = db.UpsertImages(map[string]string{"test-image": "ami-updated"}, now3)
		if err != nil {
			t.Fatalf("Failed to update image: %v", err)
		}

		imageID, resolvedAt, err = db.GetImage("test-image")
		if err != nil {
			t.Fatalf("Failed to get updated image: %v", err)
		}
		if imageID != "ami-updated" {
			t.Errorf("Expected updated image ID 'ami-updated', got '%s'", imageID)
		}
		if resolvedAt.Unix() != now3.Unix() {
			t.Errorf("Updated ResolvedAt mismatch: expected %v, got %v", now3, resolvedAt)
		}

		// Test GetImage for non-existent image
		_, _, err = db.GetImage("non-existent")
		if err == nil {
			t.Error("Expected error when getting non-existent image")
		}
	})

	t.Run("CreateInstance", func(t *testing.T) {
		instanceID, _ := puidv7.New("knc")
		nonce := "test-nonce-jwt"
		now := time.Now().UTC()

		instance := &Instance{
			ID:       instanceID,
			Tenant:   "default",
			Nonce:    nonce,
			IssuedAt: &now,
		}

		err := db.CreateInstance(instance)
		if err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		// Verify instance was created
		retrieved, err := db.GetInstance(instanceID)
		if err != nil {
			t.Fatalf("Failed to get instance: %v", err)
		}

		if retrieved.ID != instanceID {
			t.Errorf("Expected ID '%s', got '%s'", instanceID, retrieved.ID)
		}
		if retrieved.Nonce != nonce {
			t.Errorf("Expected nonce '%s', got '%s'", nonce, retrieved.Nonce)
		}
		if retrieved.RegisteredAt != nil {
			t.Error("RegisteredAt should be nil for new instance")
		}
	})

	t.Run("ValidateAgentNonce", func(t *testing.T) {
		nonce := "test-validate-agent-nonce"
		instanceID, _ := puidv7.New("knc")

		// Validate nonce that doesn't exist (should error - nonce not found)
		err := db.ValidateAgentNonce(nonce)
		if err == nil {
			t.Error("Expected error when validating non-existent nonce")
		}

		// Create instance with nonce (simulates server spawning instance)
		instance := &Instance{
			ID:       instanceID,
			Tenant:   "default",
			Nonce:    nonce,
			IssuedAt: timePtr(time.Now().UTC()),
		}
		err = db.CreateInstance(instance)
		if err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		// Validate nonce that exists but isn't registered yet (should be valid)
		err = db.ValidateAgentNonce(nonce)
		if err != nil {
			t.Errorf("Unregistered nonce should be valid: %v", err)
		}

		// Complete registration
		publicKey := []byte("test-public-key")
		err = db.CreateAgentRegistrationRecord(instanceID, nonce, publicKey)
		if err != nil {
			t.Fatalf("Failed to create registration record: %v", err)
		}

		// Validate same nonce again (should now be invalid/used)
		err = db.ValidateAgentNonce(nonce)
		if err == nil {
			t.Error("Expected error when validating used nonce")
		}
	})

	t.Run("OperatorRegistration", func(t *testing.T) {
		nonce := "test-validate-operator-nonce"
		clusterID, _ := puidv7.New("cls")

		publicKey := []byte("test-public-key")
		err := db.CreateOperatorRegistrationRecord(clusterID, "default", nonce, publicKey)
		if err != nil {
			t.Fatalf("Failed to create registration record: %v", err)
		}

		// Verify operator record can be retrieved by cluster ID
		op, err := db.GetOperatorByClusterID(clusterID)
		if err != nil {
			t.Fatalf("Failed to get operator: %v", err)
		}
		if op.ID == "" {
			t.Error("Operator ID should not be empty")
		}
		if op.ClusterID != clusterID {
			t.Errorf("Expected cluster ID '%s', got '%s'", clusterID, op.ClusterID)
		}
		if op.Tenant != "default" {
			t.Errorf("Expected tenant 'default', got '%s'", op.Tenant)
		}
		if op.Nonce != nonce {
			t.Errorf("Expected nonce '%s', got '%s'", nonce, op.Nonce)
		}

		// Verify operator record can be retrieved by ID
		op2, err := db.GetOperator(op.ID)
		if err != nil {
			t.Fatalf("Failed to get operator by ID: %v", err)
		}
		if op2.ID != op.ID {
			t.Errorf("Expected ID '%s', got '%s'", op.ID, op2.ID)
		}
	})

	t.Run("UpdateInstanceHealth", func(t *testing.T) {
		instanceID, _ := puidv7.New("knc")
		nonce := "test-health-nonce"

		instance := &Instance{
			ID:       instanceID,
			Tenant:   "default",
			Nonce:    nonce,
			IssuedAt: timePtr(time.Now().UTC()),
		}

		// Create instance
		err := db.CreateInstance(instance)
		if err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		// Update health
		healthData := []byte(`{"count": 1, "uptime": "1h30m", "files": {"ca.crt": "2024-01-01T12:00:00Z"}}`)
		err = db.UpdateInstanceHealth(instanceID, healthData)
		if err != nil {
			t.Fatalf("Failed to update instance health: %v", err)
		}

		// Verify health was updated
		retrieved, err := db.GetInstance(instanceID)
		if err != nil {
			t.Fatalf("Failed to get instance: %v", err)
		}

		if string(retrieved.Health) != string(healthData) {
			t.Errorf("Health data mismatch. Expected: %s, Got: %s", healthData, retrieved.Health)
		}
		if retrieved.UpdatedAt == nil {
			t.Error("UpdatedAt should be set after health update")
		}
	})

	t.Run("ListInstances", func(t *testing.T) {
		// Create multiple instances
		for i := 0; i < 3; i++ {
			instanceID, _ := puidv7.New("knc")
			instance := &Instance{
				ID:       instanceID,
				Tenant:   "default",
				Nonce:    fmt.Sprintf("test-list-nonce-%d", i),
				IssuedAt: timePtr(time.Now().UTC()),
			}
			err := db.CreateInstance(instance)
			if err != nil {
				t.Fatalf("Failed to create instance %d: %v", i, err)
			}
		}

		// List instances
		instances, err := db.ListInstances()
		if err != nil {
			t.Fatalf("Failed to list instances: %v", err)
		}

		if len(instances) < 3 {
			t.Errorf("Expected at least 3 instances, got %d", len(instances))
		}
	})

	t.Run("DeleteInstance", func(t *testing.T) {
		instanceID, _ := puidv7.New("knc")
		nonce := "test-delete-nonce"

		instance := &Instance{
			ID:       instanceID,
			Tenant:   "default",
			Nonce:    nonce,
			IssuedAt: timePtr(time.Now().UTC()),
		}

		// Create instance
		err := db.CreateInstance(instance)
		if err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		// Delete instance (soft delete)
		err = db.DeleteInstance(instanceID)
		if err != nil {
			t.Fatalf("Failed to delete instance: %v", err)
		}

		// Verify instance is no longer returned by GetInstance
		_, err = db.GetInstance(instanceID)
		if err == nil {
			t.Error("Expected error when getting deleted instance")
		}

		// Verify instance doesn't appear in list
		instances, err := db.ListInstances()
		if err != nil {
			t.Fatalf("Failed to list instances: %v", err)
		}

		for _, inst := range instances {
			if inst.ID == instanceID {
				t.Error("Deleted instance should not appear in list")
			}
		}
	})

	t.Run("SeedFromS3Data", func(t *testing.T) {
		// Prepare seed data
		seedInstances := []*Instance{
			{
				ID:           "knc-seed-001",
				Tenant:       "default",
				Nonce:        "seed-nonce-1",
				IssuedAt:     timePtr(time.Now()),
				RegisteredAt: timePtr(time.Now().UTC()),
			},
			{
				ID:       "knc-seed-002",
				Tenant:   "default",
				Nonce:    "seed-nonce-2",
				IssuedAt: timePtr(time.Now().UTC()),
			},
		}

		// Seed database
		err := db.SeedFromS3Data(seedInstances)
		if err != nil {
			t.Fatalf("Failed to seed database: %v", err)
		}

		// Verify seeded instances exist
		for _, seedInstance := range seedInstances {
			retrieved, err := db.GetInstance(seedInstance.ID)
			if err != nil {
				t.Errorf("Failed to get seeded instance %s: %v", seedInstance.ID, err)
			}
			if retrieved.Nonce != seedInstance.Nonce {
				t.Errorf("Seeded instance nonce mismatch for %s", seedInstance.ID)
			}
		}
	})
}

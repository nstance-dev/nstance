// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/infra/mock"
	"github.com/nstance-dev/nstance/internal/server/infra/provider"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// MockInstanceManager
type MockInstanceManager struct {
	CreateInstanceFunc func(ctx context.Context, req instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error)
	DeleteInstanceFunc func(ctx context.Context, instanceID string) error
}

func (m *MockInstanceManager) CreateInstance(ctx context.Context, req instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error) {
	if m.CreateInstanceFunc != nil {
		return m.CreateInstanceFunc(ctx, req)
	}
	return &instances.CreateInstanceResponse{InstanceID: "inst-1"}, nil
}

func (m *MockInstanceManager) DeleteInstance(ctx context.Context, instanceID string) error {
	if m.DeleteInstanceFunc != nil {
		return m.DeleteInstanceFunc(ctx, instanceID)
	}
	return nil
}

// MockStorage
type MockStorage struct {
	storage.Storage
	GetFunc func(ctx context.Context, key string) ([]byte, string, error)
	PutFunc func(ctx context.Context, key string, data []byte) error
}

func (m *MockStorage) Get(ctx context.Context, key string) ([]byte, string, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, key)
	}
	return nil, "", storage.ErrNotFound
}

func (m *MockStorage) Put(ctx context.Context, key string, data []byte) error {
	if m.PutFunc != nil {
		return m.PutFunc(ctx, key, data)
	}
	return nil
}

func (m *MockStorage) PutIfMatch(ctx context.Context, key string, data []byte, etag string) error {
	if m.PutFunc != nil {
		return m.PutFunc(ctx, key, data)
	}
	return nil
}

func TestReconciler_ScaleUp(t *testing.T) {
	// Setup
	db, err := localdb.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	mockStorage := &MockStorage{}
	loader, err := config.NewLoader(config.LoaderOptions{
		Storage:      mockStorage,
		CacheStorage: mockStorage, // Use same mock
		LocalDB:      db,
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create loader: %v", err)
	}

	// Config with one group
	cfg := &config.Config{
		Shard: config.ShardConfig{
			SubnetPools: map[string][]string{
				"primary": {"subnet-abc123"},
			},
		},
		Groups: map[string]map[string]config.GroupConfig{
			"default": {
				"group1": {
					Template:   "tpl1",
					Size:       config.IntPtr(2),
					SubnetPool: "primary",
				},
			},
		},
		Templates: map[string]config.TemplateConfig{
			"tpl1": {
				Kind:       "server",
				SubnetPool: "primary",
			},
		},
	}
	loader.SetConfig(cfg)

	createdCount := 0
	doneCh := make(chan struct{})
	mockIM := &MockInstanceManager{
		CreateInstanceFunc: func(ctx context.Context, req instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error) {
			createdCount++
			if createdCount == 2 {
				close(doneCh)
			}
			return &instances.CreateInstanceResponse{InstanceID: fmt.Sprintf("new-inst-%d", createdCount)}, nil
		},
	}

	mockProvider := mock.NewProvider(mock.Options{
		Config: provider.ProviderConfig{
			Kind:   "aws",
			Region: "us-west-2",
			Zone:   "us-west-2a",
		},
	})

	r, err := New(Options{
		InstanceManager: mockIM,
		ConfigLoader:    loader,
		LocalDB:         db,
		Provider:        mockProvider,
		NotifyDrain:     func(id, g, r string, u, d time.Time) {},
		IsLeader:        func() bool { return true },
		Logger:          slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create reconciler: %v", err)
	}

	err = r.Start(context.Background())
	if err != nil {
		t.Fatalf("Failed to start reconciler: %v", err)
	}
	defer r.Stop()

	r.Enqueue(ReconcileEvent{
		Type:     EventGroupChanged,
		Tenant:   "default",
		GroupKey: "group1",
	})

	select {
	case <-doneCh:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for scale up")
	}
}

func TestReconciler_ScaleDown(t *testing.T) {
	// Setup
	db, err := localdb.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Insert existing instances into DB
	now := time.Now().UTC()
	providerState := []byte(`{"status":"running"}`)
	pid1 := "i-001"
	pid2 := "i-002"
	pid3 := "i-003"
	inst1 := &localdb.Instance{
		ID:            "inst-1",
		Tenant:        "default",
		Group:         "group1",
		ProviderID:    &pid1,
		CreatedAt:     now.Add(-10 * time.Minute),
		Nonce:         "nonce-1",
		ProviderState: providerState,
	}
	inst2 := &localdb.Instance{
		ID:            "inst-2",
		Tenant:        "default",
		Group:         "group1",
		ProviderID:    &pid2,
		CreatedAt:     now.Add(-5 * time.Minute),
		Nonce:         "nonce-2",
		ProviderState: providerState,
	}
	inst3 := &localdb.Instance{
		ID:            "inst-3",
		Tenant:        "default",
		Group:         "group1",
		ProviderID:    &pid3,
		CreatedAt:     now,
		Nonce:         "nonce-3",
		ProviderState: providerState,
	}
	if err := db.CreateInstance(inst1); err != nil {
		t.Fatalf("Failed to create inst1: %v", err)
	}
	if err := db.CreateInstance(inst2); err != nil {
		t.Fatalf("Failed to create inst2: %v", err)
	}
	if err := db.CreateInstance(inst3); err != nil {
		t.Fatalf("Failed to create inst3: %v", err)
	}

	mockStorage := &MockStorage{}
	loader, err := config.NewLoader(config.LoaderOptions{
		Storage:      mockStorage,
		CacheStorage: mockStorage, // Use same mock
		LocalDB:      db,
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create loader: %v", err)
	}

	// Config with one group size 1 (should delete 2 instances)
	cfg := &config.Config{
		Shard: config.ShardConfig{
			SubnetPools: map[string][]string{
				"primary": {"subnet-abc123"},
			},
		},
		Groups: map[string]map[string]config.GroupConfig{
			"default": {
				"group1": {
					Template:   "tpl1",
					Size:       config.IntPtr(1),
					SubnetPool: "primary",
				},
			},
		},
		Templates: map[string]config.TemplateConfig{
			"tpl1": {
				Kind:       "server",
				SubnetPool: "primary",
			},
		},
	}
	loader.SetConfig(cfg)

	deletedCount := 0
	doneCh := make(chan struct{})
	mockIM := &MockInstanceManager{
		DeleteInstanceFunc: func(ctx context.Context, instanceID string) error {
			deletedCount++
			// Expect inst-1 and inst-2 to be deleted (oldest first)
			if instanceID != "inst-1" && instanceID != "inst-2" {
				t.Errorf("Unexpected instance deleted: %s", instanceID)
			}
			if deletedCount == 2 {
				close(doneCh)
			}
			return nil
		},
	}

	mockProvider := mock.NewProvider(mock.Options{
		Config: provider.ProviderConfig{
			Kind:   "aws",
			Region: "us-west-2",
			Zone:   "us-west-2a",
		},
	})

	r, err := New(Options{
		InstanceManager: mockIM,
		ConfigLoader:    loader,
		LocalDB:         db,
		Provider:        mockProvider,
		NotifyDrain:     func(id, g, r string, u, d time.Time) {},
		IsLeader:        func() bool { return true },
		Logger:          slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create reconciler: %v", err)
	}

	err = r.Start(context.Background())
	if err != nil {
		t.Fatalf("Failed to start reconciler: %v", err)
	}
	defer r.Stop()

	r.Enqueue(ReconcileEvent{
		Type:     EventGroupChanged,
		Tenant:   "default",
		GroupKey: "group1",
	})

	select {
	case <-doneCh:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for scale down")
	}
}

// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// mockInstanceManager implements InstanceManager interface for testing
type mockInstanceManager struct{}

func (m *mockInstanceManager) CreateInstance(ctx context.Context, req instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error) {
	return &instances.CreateInstanceResponse{
		InstanceID:         "test-instance-id",
		Group:              req.Group,
		Template:           req.Template,
		ProviderInstanceID: "i-test123",
		Status:             "running",
	}, nil
}

func (m *mockInstanceManager) DeleteInstance(ctx context.Context, instanceID string) error {
	return nil
}

func (m *mockInstanceManager) GetInstanceStatus(ctx context.Context, instanceID string) (*instances.InstanceStatus, error) {
	return &instances.InstanceStatus{
		InstanceID:         instanceID,
		Group:              "test-group",
		ProviderInstanceID: "i-test123",
		Status:             "running",
		CreatedAt:          time.Now().UTC().Add(-1 * time.Hour),
		LastUpdated:        time.Now().UTC(),
	}, nil
}

func TestService(t *testing.T) {
	ctx := context.Background()

	// Create test configuration
	testConfig := &config.Config{
		Cluster: config.ClusterConfig{
			ID: "example-cluster",
			Secrets: config.SecretsConfig{
				Provider: "memory",
			},
		},
		Shard: config.ShardConfig{
			ID: "test-shard",
			Infra: config.InfraConfig{
				Provider: "aws",
				Region:   "us-west-2",
				Zone:     "us-west-2a",
			},
			Bind: config.BindConfig{
				HealthAddr:       "127.0.0.1:8990",
				ElectionAddr:     "127.0.0.1:8991",
				RegistrationAddr: "127.0.0.1:8992",
				OperatorAddr:     "127.0.0.1:8993",
				AgentAddr:        "127.0.0.1:8994",
			},
			Advertise: config.AdvertiseConfig{
				HealthAddr:       "127.0.0.1:8990",
				ElectionAddr:     "127.0.0.1:8991",
				RegistrationAddr: "127.0.0.1:8992",
				OperatorAddr:     "127.0.0.1:8993",
				AgentAddr:        "127.0.0.1:8994",
			},
		},
		Templates: map[string]config.TemplateConfig{
			"test": {
				Kind: "test",
				Arch: "amd64",
			},
		},
	}
	testConfig.SetDefaults()

	// Create config loader
	mainStorage := storage.NewMock()
	cacheStorage := storage.NewMock()

	// Create test database
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	testDB, err := localdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	t.Cleanup(func() {
		_ = testDB.Close()
	})

	configLoader, err := config.NewLoader(config.LoaderOptions{
		Storage:      mainStorage,
		CacheStorage: cacheStorage,
		LocalDB:      testDB,
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create config loader: %v", err)
	}

	// Put initial config in storage with correct key
	configKey := "config.jsonc"
	configData := []byte(`{"cluster":{"id":"example-cluster","secrets":{"provider":"memory"}},"shard":{"id":"test-shard","infra":{"provider":"aws","region":"us-west-2","zone":"us-west-2a"},"bind":{"health_addr":"0.0.0.0:8990","election_addr":"0.0.0.0:8991","registration_addr":"0.0.0.0:8992","operator_addr":"0.0.0.0:8993","agent_addr":"0.0.0.0:8994"},"advertise":{"health_addr":"127.0.0.1:8990","election_addr":"127.0.0.1:8991","registration_addr":"127.0.0.1:8992","operator_addr":"127.0.0.1:8993","agent_addr":"127.0.0.1:8994"},"subnet_pools":{"default":["subnet-123"]}},"templates":{"test":{"kind":"test","arch":"amd64","subnet_pool":"default"}}}`)
	err = mainStorage.Put(ctx, configKey, configData)
	if err != nil {
		t.Fatalf("Failed to put initial config: %v", err)
	}

	// Load config into loader (this will also set metadata)
	_, err = configLoader.LoadConfigAndGroups(ctx, false)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Track events
	var changedGroups []string
	var ackedDrains []string

	onGroupChanged := func(tenant, groupKey string) {
		changedGroups = append(changedGroups, tenant+"/"+groupKey)
	}

	onDrainAcked := func(instanceID string) {
		ackedDrains = append(ackedDrains, instanceID)
	}

	// Create mock database
	mockDB, err := localdb.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to open mock DB: %v", err)
	}
	defer func() { _ = mockDB.Close() }()

	// Create mock instance manager
	mockInstanceManager := &mockInstanceManager{}

	// Create operator service
	operatorService, err := New(Options{
		ConfigLoader:    configLoader,
		LocalDB:         mockDB,
		InstanceManager: mockInstanceManager,
		OnGroupChanged:  onGroupChanged,
		OnDrainAcked:    onDrainAcked,
		Logger:          slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create operator service: %v", err)
	}

	t.Run("GetConfigStatus", func(t *testing.T) {
		// Create context with client info
		clientInfo := &api.ClientInfo{
			ClientID: "test-operator",
			Role:     "operator",
		}
		ctxWithClient := context.WithValue(ctx, api.ClientInfoKey, clientInfo)

		// Get config status
		resp, err := operatorService.GetConfigStatus(ctxWithClient, &emptypb.Empty{})
		if err != nil {
			t.Fatalf("Failed to get config status: %v", err)
		}

		// Verify response
		if resp.Etag == "" {
			t.Error("ETag should not be empty")
		}
		if resp.LastModified == nil {
			t.Error("LastModified should not be nil")
		}
		if resp.Size <= 0 {
			t.Error("Size should be positive")
		}
	})

	t.Run("InstanceManagement", func(t *testing.T) {
		// Create context with client info
		clientInfo := &api.ClientInfo{
			ClientID: "test-operator",
			Role:     "operator",
		}
		ctxWithClient := context.WithValue(ctx, api.ClientInfoKey, clientInfo)

		// Test CreateInstance
		createResp, err := operatorService.CreateInstance(ctxWithClient, &proto.CreateInstanceRequest{
			InstanceId: "test-instance",
			Config: &proto.InstanceConfig{
				Group: "test-group",
			},
		})
		if err != nil {
			t.Errorf("CreateInstance failed: %v", err)
		}
		if createResp == nil {
			t.Error("Expected create instance response")
		}

		// Test DeleteInstance
		_, err = operatorService.DeleteInstance(ctxWithClient, &proto.DeleteInstanceRequest{
			InstanceId: "test-instance",
		})
		if err != nil {
			t.Errorf("DeleteInstance failed: %v", err)
		}

		// Test GetInstanceStatus
		statusResp, err := operatorService.GetInstanceStatus(ctxWithClient, &proto.GetInstanceStatusRequest{
			InstanceId: "test-instance",
		})
		if err != nil {
			t.Errorf("GetInstanceStatus failed: %v", err)
		}
		if statusResp == nil {
			t.Error("Expected instance status response")
		}
	})
}

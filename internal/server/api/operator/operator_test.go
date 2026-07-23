// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// mockInstanceManager implements InstanceManager interface for testing
type mockInstanceManager struct {
	lastCreateRequest instances.CreateInstanceRequest
	instanceTenants   map[string]string
	deleteCalls       int
	statusCalls       int
}

// CreateInstance records a test request and returns a successful instance result.
func (m *mockInstanceManager) CreateInstance(ctx context.Context, req instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error) {
	m.lastCreateRequest = req
	return &instances.CreateInstanceResponse{
		InstanceID:         "test-instance-id",
		Group:              req.Group,
		Template:           req.Template,
		ProviderInstanceID: "i-test123",
		Status:             "running",
	}, nil
}

// recordingServerStream records typed events sent by a server-streaming RPC.
type recordingServerStream[T any] struct {
	grpc.ServerStream
	ctx    context.Context
	events []*T
}

// Context returns the stream's test context.
func (m *recordingServerStream[T]) Context() context.Context { return m.ctx }

// Send records an event.
func (m *recordingServerStream[T]) Send(event *T) error {
	m.events = append(m.events, event)
	return nil
}

// DeleteInstance records an authorized test deletion request.
func (m *mockInstanceManager) DeleteInstance(_ context.Context, tenant, instanceID string) error {
	if err := m.ValidateInstanceTenant(tenant, instanceID); err != nil {
		return err
	}
	m.deleteCalls++
	return nil
}

// GetInstanceStatus records an authorized status lookup and returns a test status.
func (m *mockInstanceManager) GetInstanceStatus(_ context.Context, tenant, instanceID string) (*instances.InstanceStatus, error) {
	if err := m.ValidateInstanceTenant(tenant, instanceID); err != nil {
		return nil, err
	}
	m.statusCalls++
	return &instances.InstanceStatus{
		InstanceID:         instanceID,
		Group:              "test-group",
		ProviderInstanceID: "i-test123",
		Status:             "running",
		CreatedAt:          time.Now().UTC().Add(-1 * time.Hour),
		LastUpdated:        time.Now().UTC(),
	}, nil
}

// ValidateInstanceTenant verifies test instance ownership.
func (m *mockInstanceManager) ValidateInstanceTenant(tenant, instanceID string) error {
	instanceTenant, exists := m.instanceTenants[instanceID]
	if !exists {
		return sql.ErrNoRows
	}
	if instanceTenant != tenant {
		return instances.ErrInstanceTenantMismatch
	}
	return nil
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
	configData := []byte(`{"cluster":{"id":"example-cluster","secrets":{"provider":"memory"}},"shard":{"id":"test-shard","infra":{"provider":"aws","region":"us-west-2","zone":"us-west-2a"},"bind":{"health_addr":"0.0.0.0:8990","election_addr":"0.0.0.0:8991","registration_addr":"0.0.0.0:8992","operator_addr":"0.0.0.0:8993","agent_addr":"0.0.0.0:8994"},"advertise":{"health_addr":"127.0.0.1:8990","election_addr":"127.0.0.1:8991","registration_addr":"127.0.0.1:8992","operator_addr":"127.0.0.1:8993","agent_addr":"127.0.0.1:8994"},"subnet_pools":{"default":["subnet-123"]}},"templates":{"test":{"kind":"test","arch":"amd64","subnet_pool":"default"}},"groups":{"default":{"main":{"template":"test","size":1,"subnet_pool":"default","vars":{"mode":"static"}}}}}`)
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

	onDrainAcked := func(tenant, instanceID string) {
		ackedDrains = append(ackedDrains, tenant+"/"+instanceID)
	}

	// Create mock database
	mockDB, err := localdb.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to open mock DB: %v", err)
	}
	defer func() { _ = mockDB.Close() }()

	// Create mock instance manager
	mockInstanceManager := &mockInstanceManager{instanceTenants: map[string]string{
		"test-instance": "red",
		"blue-instance": "blue",
	}}

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
			Tenant:   "red",
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
		if mockInstanceManager.lastCreateRequest.Tenant != "red" {
			t.Errorf("CreateInstance tenant = %q, want red", mockInstanceManager.lastCreateRequest.Tenant)
		}
		if err := mockDB.CreateInstance(&localdb.Instance{
			ID:        "test-instance",
			Tenant:    "red",
			Group:     "test-group",
			Nonce:     "test-instance-nonce",
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create test instance record: %v", err)
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

	t.Run("InstanceManagementRejectsOtherTenant", func(t *testing.T) {
		if err := mockDB.CreateInstance(&localdb.Instance{
			ID:        "blue-instance",
			Tenant:    "blue",
			Group:     "workers",
			Nonce:     "blue-instance-nonce",
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create blue instance record: %v", err)
		}
		clientCtx := context.WithValue(ctx, api.ClientInfoKey, &api.ClientInfo{
			ClientID: "red-operator",
			Role:     "operator",
			Tenant:   "red",
		})
		statusCalls := mockInstanceManager.statusCalls
		deleteCalls := mockInstanceManager.deleteCalls
		ackedCount := len(ackedDrains)

		_, err := operatorService.GetInstanceStatus(clientCtx, &proto.GetInstanceStatusRequest{InstanceId: "blue-instance"})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("GetInstanceStatus error = %v, want PermissionDenied", err)
		}
		_, err = operatorService.DeleteInstance(clientCtx, &proto.DeleteInstanceRequest{InstanceId: "blue-instance"})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("DeleteInstance error = %v, want PermissionDenied", err)
		}
		_, err = operatorService.AcknowledgeDrained(clientCtx, &proto.DrainAckRequest{InstanceId: "blue-instance"})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("AcknowledgeDrained error = %v, want PermissionDenied", err)
		}
		if mockInstanceManager.statusCalls != statusCalls || mockInstanceManager.deleteCalls != deleteCalls || len(ackedDrains) != ackedCount {
			t.Fatal("foreign-tenant request reached an instance action")
		}
	})

	t.Run("LiveEventsAreTenantScoped", func(t *testing.T) {
		redGroups := &recordingServerStream[proto.GroupEvent]{ctx: ctx}
		blueGroups := &recordingServerStream[proto.GroupEvent]{ctx: ctx}
		redErrors := &recordingServerStream[proto.ErrorEvent]{ctx: ctx}
		blueErrors := &recordingServerStream[proto.ErrorEvent]{ctx: ctx}
		redInstances := &recordingServerStream[proto.InstanceEvent]{ctx: ctx}
		blueInstances := &recordingServerStream[proto.InstanceEvent]{ctx: ctx}
		operatorService.groupsStreams["red"] = &groupsStream{stream: redGroups}
		operatorService.groupsStreams["blue"] = &groupsStream{stream: blueGroups}
		operatorService.errorsStreams["red"] = redErrors
		operatorService.errorsStreams["blue"] = blueErrors
		operatorService.instancesStreams["red"] = &instancesStream{stream: redInstances}
		operatorService.instancesStreams["blue"] = &instancesStream{stream: blueInstances}

		providerID := "provider-red"
		if err := mockDB.CreateInstance(&localdb.Instance{
			ID:         "red-instance",
			Tenant:     "red",
			Group:      "workers",
			ProviderID: &providerID,
			Nonce:      "red-instance-nonce",
			CreatedAt:  time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create red instance: %v", err)
		}

		operatorService.NotifyGroupEvent("red", &proto.GroupEvent{
			Type:  proto.GroupEvent_UPSERT,
			Group: &proto.GroupStatus{Tenant: "red", Key: "workers"},
		})
		operatorService.NotifyError("blue", &proto.ErrorEvent{Group: "workers", Error: "failed"})
		operatorService.NotifyDrain(DrainNotification{InstanceID: "red-instance", Group: "workers"})

		if len(redGroups.events) != 1 || len(blueGroups.events) != 0 {
			t.Fatalf("red group event delivery: red=%d blue=%d", len(redGroups.events), len(blueGroups.events))
		}
		if len(redErrors.events) != 0 || len(blueErrors.events) != 1 {
			t.Fatalf("blue error event delivery: red=%d blue=%d", len(redErrors.events), len(blueErrors.events))
		}
		if len(redInstances.events) != 1 || len(blueInstances.events) != 0 {
			t.Fatalf("red instance event delivery: red=%d blue=%d", len(redInstances.events), len(blueInstances.events))
		}
	})

	t.Run("DeletingStaticOverrideEmitsUpsert", func(t *testing.T) {
		overrideSize := 3
		if err := config.UpsertGroup(ctx, configLoader, "default", "main", config.GroupConfig{
			Size: &overrideSize,
			Vars: map[string]string{"mode": "dynamic", "extra": "override"},
		}); err != nil {
			t.Fatalf("UpsertGroup override: %v", err)
		}

		stream := &recordingServerStream[proto.GroupEvent]{ctx: ctx}
		operatorService.groupsStreams["default"] = &groupsStream{stream: stream}
		clientCtx := context.WithValue(ctx, api.ClientInfoKey, &api.ClientInfo{
			ClientID: "test-operator",
			Role:     "operator",
			Tenant:   "default",
		})
		if _, err := operatorService.DeleteGroup(clientCtx, &proto.DeleteGroupRequest{Key: "main"}); err != nil {
			t.Fatalf("DeleteGroup: %v", err)
		}

		if len(stream.events) != 1 {
			t.Fatalf("group events = %d, want 1", len(stream.events))
		}
		event := stream.events[0]
		if event.Type != proto.GroupEvent_UPSERT || event.Group.Tenant != "default" || event.Group.Key != "main" || event.Group.Size != 1 || !event.Group.IsStatic {
			t.Fatalf("unexpected restored static group event: %#v", event)
		}
		if len(event.Group.Vars) != 1 || event.Group.Vars["mode"] != "static" {
			t.Fatalf("restored static vars = %v, want map[mode:static]", event.Group.Vars)
		}
	})

	t.Run("RefreshReconcilesRemovedGroupWithManagedInstances", func(t *testing.T) {
		if err := mockDB.CreateInstance(&localdb.Instance{
			ID:            "removed-group-instance",
			Tenant:        "default",
			Group:         "main",
			Nonce:         "removed-group-instance-nonce",
			ProviderState: []byte(`{"status":"running"}`),
			CreatedAt:     time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create removed group instance: %v", err)
		}
		refreshedConfigData := []byte(`{"cluster":{"id":"example-cluster","secrets":{"provider":"memory"}},"shard":{"id":"test-shard","infra":{"provider":"aws","region":"us-west-2","zone":"us-west-2a"},"bind":{"health_addr":"0.0.0.0:8990","election_addr":"0.0.0.0:8991","registration_addr":"0.0.0.0:8992","operator_addr":"0.0.0.0:8993","agent_addr":"0.0.0.0:8994"},"advertise":{"health_addr":"127.0.0.1:8990","election_addr":"127.0.0.1:8991","registration_addr":"127.0.0.1:8992","operator_addr":"127.0.0.1:8993","agent_addr":"127.0.0.1:8994"},"subnet_pools":{"default":["subnet-123"]}},"templates":{"test":{"kind":"test","arch":"amd64","subnet_pool":"default"}}}`)
		if err := mainStorage.Put(ctx, configKey, refreshedConfigData); err != nil {
			t.Fatalf("put refreshed config: %v", err)
		}
		clientCtx := context.WithValue(ctx, api.ClientInfoKey, &api.ClientInfo{
			ClientID: "test-operator",
			Role:     "operator",
			Tenant:   "default",
		})
		start := len(changedGroups)
		response, err := operatorService.RefreshConfig(clientCtx, &emptypb.Empty{})
		if err != nil {
			t.Fatalf("RefreshConfig: %v", err)
		}
		if !response.Updated {
			t.Fatal("RefreshConfig reported unchanged config")
		}
		found := false
		for _, group := range changedGroups[start:] {
			if group == "default/main" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("removed group was not reconciled: %v", changedGroups[start:])
		}
	})

}

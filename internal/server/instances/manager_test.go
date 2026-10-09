// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package instances

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/puidv7/puidv7-go"

	"github.com/nstance-dev/nstance/v2/internal/server/config"
	"github.com/nstance-dev/nstance/v2/internal/server/infra"
	"github.com/nstance-dev/nstance/v2/internal/server/infra/mock"
	"github.com/nstance-dev/nstance/v2/internal/server/keys"
	"github.com/nstance-dev/nstance/v2/internal/server/localdb"
	"github.com/nstance-dev/nstance/v2/internal/server/pki"
	"github.com/nstance-dev/nstance/v2/internal/server/secrets"
	"github.com/nstance-dev/nstance/v2/internal/server/storage"
)

// blockingLBProvider makes a target withdrawal observable while delegating
// all provider behavior to the embedded provider.
type blockingLBProvider struct {
	infra.Provider
	deregisterEntered chan struct{}
	deregisterRelease chan struct{}
	registrations     atomic.Int32
}

// RegisterWithLB records and delegates a target registration.
func (p *blockingLBProvider) RegisterWithLB(ctx context.Context, req infra.RegisterLBRequest) error {
	p.registrations.Add(1)
	return p.Provider.RegisterWithLB(ctx, req)
}

// DeregisterFromLB blocks until the test permits the target withdrawal.
func (p *blockingLBProvider) DeregisterFromLB(ctx context.Context, req infra.DeregisterLBRequest) error {
	close(p.deregisterEntered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.deregisterRelease:
	}
	return p.Provider.DeregisterFromLB(ctx, req)
}

// TestReconcileLoadBalancersHonorsWithdrawalBoundary verifies a health report
// queued behind withdrawal cannot re-add the target until restoration begins.
func TestReconcileLoadBalancersHonorsWithdrawalBoundary(t *testing.T) {
	ctx := context.Background()
	db, err := localdb.Open(filepath.Join(t.TempDir(), "instances.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	providerID := "provider-1"
	if err := db.CreateInstance(&localdb.Instance{ID: "instance-1", Tenant: "red", Group: "web", ProviderID: &providerID, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	loaderDB, err := localdb.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loaderDB.Close() })
	loader, err := config.NewLoader(config.LoaderOptions{Storage: storage.NewMock(), CacheStorage: storage.NewMock(), LocalDB: loaderDB, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	lb := config.LoadBalancerConfig{Provider: "aws", TargetGroups: []config.AWSTargetGroupConfig{{ARN: "target-group", TargetPort: 443}}}
	loader.SetConfig(&config.Config{
		Shard:         config.ShardConfig{Infra: config.InfraConfig{Zone: "zone-a"}},
		Groups:        map[string]map[string]config.GroupConfig{"red": {"web": {LoadBalancers: []string{"public"}}}},
		LoadBalancers: map[string]config.LoadBalancerConfig{"public": lb},
	})
	delegate := mock.NewProvider(mock.Options{Config: infra.ProviderConfig{Kind: "mock"}, Logger: slog.Default()})
	provider := &blockingLBProvider{Provider: delegate, deregisterEntered: make(chan struct{}), deregisterRelease: make(chan struct{})}
	var blocked atomic.Bool
	blocked.Store(true)
	manager := &Manager{configLoader: loader, localDB: db, provider: provider, logger: slog.Default(), targetRegistrationBlocked: func(string) bool { return blocked.Load() }}
	req := infra.RegisterLBRequest{ProviderInstanceID: providerID, LBConfig: infra.LoadBalancerConfigForProvider(lb), Zone: "zone-a"}
	if err := delegate.RegisterWithLB(ctx, req); err != nil {
		t.Fatal(err)
	}
	withdrawn := make(chan error, 1)
	go func() { withdrawn <- manager.DeregisterTarget(ctx, infra.DeregisterLBRequest(req)) }()
	<-provider.deregisterEntered
	reconciled := make(chan error, 1)
	go func() { reconciled <- manager.ReconcileLoadBalancers(ctx, "instance-1") }()
	close(provider.deregisterRelease)
	if err := <-withdrawn; err != nil {
		t.Fatal(err)
	}
	if err := <-reconciled; err != nil {
		t.Fatal(err)
	}
	if got := provider.registrations.Load(); got != 0 {
		t.Fatalf("registrations while blocked = %d, want 0", got)
	}
	state, err := delegate.GetLBTargetState(ctx, req)
	if err != nil || state != infra.LBTargetDeregistered {
		t.Fatalf("target state while blocked = %q, %v", state, err)
	}
	blocked.Store(false)
	if err := manager.ReconcileLoadBalancers(ctx, "instance-1"); err != nil {
		t.Fatal(err)
	}
	if got := provider.registrations.Load(); got != 1 {
		t.Fatalf("registrations after restoration intent = %d, want 1", got)
	}
}

// targetStateProvider holds a target's reported state while delegating VM lifecycle.
type targetStateProvider struct {
	infra.Provider
	state infra.LBTargetState
}

// GetLBTargetState reports the target state selected by the test.
func (p *targetStateProvider) GetLBTargetState(context.Context, infra.RegisterLBRequest) (infra.LBTargetState, error) {
	return p.state, nil
}

// TestDeleteInstanceDrainBarrier verifies only committed sleep permits VM
// termination during draining, and unknown or still-routable targets block it.
func TestDeleteInstanceDrainBarrier(t *testing.T) {
	for _, tc := range []struct {
		name   string
		asleep bool
		state  infra.LBTargetState
		delete bool
	}{
		{"awake-draining", false, infra.LBTargetDraining, false},
		{"asleep-draining", true, infra.LBTargetDraining, true},
		{"asleep-healthy", true, infra.LBTargetHealthy, false},
		{"asleep-partial", true, infra.LBTargetPartial, false},
		{"asleep-unknown", true, "", false},
		{"awake-deregistered", false, infra.LBTargetDeregistered, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := localdb.Open(filepath.Join(t.TempDir(), "instances.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			store := storage.NewMock()
			loader, err := config.NewLoader(config.LoaderOptions{Storage: store, CacheStorage: storage.NewMock(), LocalDB: db, Logger: slog.Default()})
			if err != nil {
				t.Fatal(err)
			}
			loader.SetConfig(&config.Config{LoadBalancers: map[string]config.LoadBalancerConfig{
				"public": {Provider: "aws", TargetGroups: []config.AWSTargetGroupConfig{{ARN: "target-group", TargetPort: 443}}},
			}})
			delegate := mock.NewProvider(mock.Options{Config: infra.ProviderConfig{Kind: "mock"}, Logger: slog.Default()})
			id, err := puidv7.New("dft")
			if err != nil {
				t.Fatal(err)
			}
			created, err := delegate.CreateInstance(ctx, infra.CreateInstanceRequest{InstanceID: id})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.CreateInstance(&localdb.Instance{ID: id, Tenant: "red", Group: "web", ProviderID: &created.ProviderInstanceID, CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			if err := db.UpsertLBInstance("public", id, localdb.LBStatusRegistered); err != nil {
				t.Fatal(err)
			}
			manager := &Manager{
				configLoader: loader, localDB: db, storage: store, logger: slog.Default(),
				provider:     &targetStateProvider{Provider: delegate, state: tc.state},
				tenantAsleep: func(tenant string) bool { return tc.asleep && tenant == "red" },
			}
			if err := manager.DeleteInstance(ctx, "red", id); (err == nil) != tc.delete {
				t.Fatalf("DeleteInstance error=%v, want deletion=%t", err, tc.delete)
			}
			status, err := delegate.GetInstanceStatus(ctx, id, created.ProviderInstanceID)
			if err != nil {
				t.Fatal(err)
			}
			deleted := status.Status == infra.StatusDeleting || status.Status == infra.StatusDeleted
			if deleted != tc.delete {
				t.Fatalf("provider status=%s, want deletion=%t", status.Status, tc.delete)
			}
		})
	}
}

// TestSelectSubnetFillsThenBalances verifies the /26 placement boundary and
// the post-boundary balancing rule use durable instance placement.
func TestSelectSubnetFillsThenBalances(t *testing.T) {
	db, err := localdb.Open(filepath.Join(t.TempDir(), "placement.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for i := range 53 {
		id := fmt.Sprintf("a-%d", i)
		if err := db.CreateInstance(&localdb.Instance{ID: id, Tenant: "red", Group: "workers", SubnetID: "subnet-a", Nonce: id, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	provider := mock.NewProvider(mock.Options{Config: infra.ProviderConfig{Kind: "mock"}, Logger: slog.Default()})
	manager := &Manager{localDB: db, provider: provider, logger: slog.Default()}
	cfg := &config.Config{Shard: config.ShardConfig{SubnetPools: map[string][]string{"nodes": {"subnet-b", "subnet-a"}}}}
	subnet, _, err := manager.selectSubnetWithCapacity(context.Background(), cfg, "red", "nodes", false)
	if err != nil {
		t.Fatal(err)
	}
	if subnet != "subnet-b" {
		t.Fatalf("fill selection = %q, want subnet-b", subnet)
	}
	for i := range 53 {
		id := fmt.Sprintf("b-%d", i)
		if err := db.CreateInstance(&localdb.Instance{ID: id, Tenant: "red", Group: "workers", SubnetID: "subnet-b", Nonce: id, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CreateInstance(&localdb.Instance{ID: "a-extra", Tenant: "red", Group: "workers", SubnetID: "subnet-a", Nonce: "a-extra", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	subnet, _, err = manager.selectSubnetWithCapacity(context.Background(), cfg, "red", "nodes", false)
	if err != nil {
		t.Fatal(err)
	}
	if subnet != "subnet-b" {
		t.Fatalf("balanced selection = %q, want subnet-b", subnet)
	}
	subnet, _, err = manager.selectSubnetWithCapacity(context.Background(), cfg, "red", "nodes", true)
	if err != nil {
		t.Fatal(err)
	}
	if subnet != "subnet-b" {
		t.Fatalf("populated selection = %q, want subnet-b", subnet)
	}
}

func TestInstanceManager(t *testing.T) {
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
				Provider: "mock",
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
				HealthAddr:       "172.16.0.1:8990",
				ElectionAddr:     "172.16.0.1:8991",
				RegistrationAddr: "172.16.0.1:8992",
				OperatorAddr:     "172.16.0.1:8993",
				AgentAddr:        "172.16.0.1:8994",
			},
			SubnetPools: map[string][]string{
				"primary":   {"subnet-12345678"},
				"secondary": {"subnet-87654321"},
			},
		},
		Defaults: config.DefaultsConfig{
			Vars: map[string]string{
				"Environment": "test",
			},
		},
		Templates: map[string]config.TemplateConfig{
			"knc": {
				Kind:         "knc",
				Arch:         "amd64",
				InstanceType: "t3.medium",
				SubnetPool:   "primary",
				Userdata:     &config.UserdataConfig{Content: "#!/bin/bash\necho 'Hello from {{ .Instance.ID }}'\necho 'Nonce: {{ .Nonce }}'"},
				Vars: map[string]string{
					"InstanceKind": "worker",
				},
			},
		},
		Groups: map[string]map[string]config.GroupConfig{
			"default": {
				"test-group": {
					Template:     "knc",
					Size:         config.IntPtr(3),
					InstanceType: "t3.large",
					SubnetPool:   "secondary",
					Vars: map[string]string{
						"GroupType": "test",
					},
				},
			},
		},
	}
	testConfig.SetDefaults()

	// Create secrets store with test data
	secretsStore := secrets.NewMemoryStore()

	// Generate registration nonce key
	_, noncePrivateKeyPEM, err := GenerateEd25519KeyPairForTesting()
	if err != nil {
		t.Fatalf("Failed to generate nonce key: %v", err)
	}
	if err := secretsStore.Set(ctx, "registration-nonce.key", noncePrivateKeyPEM); err != nil {
		t.Fatalf("Failed to store registration nonce key: %v", err)
	}

	// Create temporary SQLite database
	tempFile := "/tmp/nstance-instance-test.db"
	defer func() { _ = os.Remove(tempFile) }()

	localDB, err := localdb.Open(tempFile)
	if err != nil {
		t.Fatalf("Failed to open local database: %v", err)
	}
	defer func() { _ = localDB.Close() }()

	// Create test database for config loader
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	testDB, err := localdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	t.Cleanup(func() {
		_ = testDB.Close()
	})

	// Create config loader
	configLoader, err := config.NewLoader(config.LoaderOptions{
		Storage:      storage.NewMock(),
		CacheStorage: storage.NewMock(),
		LocalDB:      testDB,
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create config loader: %v", err)
	}

	// Load config
	configLoader.SetConfig(testConfig)

	// Create mock provider
	mockProvider := mock.NewProvider(mock.Options{
		Config: infra.ProviderConfig{
			Kind:   "mock",
			Region: "us-west-2",
			Zone:   "us-west-2a",
		},
		Logger: slog.Default(),
	})

	// Create mock storage
	mockStorage := storage.NewMock()

	// Generate and store CA certificate
	caCertPEM, _, err := pki.GenerateTestCA()
	if err != nil {
		t.Fatalf("Failed to generate test CA: %v", err)
	}
	// Create instance manager
	manager, err := NewManager(ManagerOptions{
		ConfigLoader: configLoader,
		SecretsStore: secretsStore,
		Storage:      mockStorage,
		LocalDB:      localDB,
		Provider:     mockProvider,
		CACert:       caCertPEM,
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create instance manager: %v", err)
	}

	t.Run("CreateInstance", func(t *testing.T) {
		instanceID, _ := puidv7.New("knc")
		req := CreateInstanceRequest{
			InstanceID:   instanceID,
			Tenant:       "default",
			Group:        "test-group", // Use the existing group from the test config
			Template:     "",           // Use group's template (knc)
			InstanceType: "t3.large",   // Override template default
			SubnetPool:   "primary",    // Override with subnet pool
			Vars: map[string]string{
				"CustomVar": "custom-value",
			},
			Tags: map[string]string{
				"Environment": "test",
				"Owner":       "test-user",
			},
		}

		resp, err := manager.CreateInstance(ctx, req)
		if err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		// Verify response
		if resp.InstanceID != req.InstanceID {
			t.Errorf("Expected instance ID '%s', got '%s'", req.InstanceID, resp.InstanceID)
		}
		if resp.ProviderInstanceID == "" {
			t.Error("Provider instance ID should not be empty")
		}
		if resp.Status != infra.StatusPending {
			t.Errorf("Expected status '%s', got '%s'", infra.StatusPending, resp.Status)
		}
		if resp.RegistrationJWT == "" {
			t.Error("Registration JWT should not be empty")
		}

		// Verify instance record was stored
		storedRecord, err := manager.getInstanceRecord(ctx, req.Tenant, req.InstanceID)
		if err != nil {
			t.Fatalf("Failed to get stored instance record: %v", err)
		}

		// Template is no longer stored in the record, it's derived from the group
		if storedRecord.InstanceType != req.InstanceType {
			t.Errorf("Expected instance type '%s', got '%s'", req.InstanceType, storedRecord.InstanceType)
		}
	})

	t.Run("GetInstanceStatus", func(t *testing.T) {
		instanceID, _ := puidv7.New("knc")

		// Create instance first
		req := CreateInstanceRequest{
			InstanceID: instanceID,
			Tenant:     "default",
			Group:      "test-group",
			Template:   "",
		}

		_, err := manager.CreateInstance(ctx, req)
		if err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		// Get status
		status, err := manager.GetInstanceStatus(ctx, "default", instanceID)
		if err != nil {
			t.Fatalf("Failed to get instance status: %v", err)
		}
		if _, err := manager.GetInstanceStatus(ctx, "other", instanceID); !errors.Is(err, ErrInstanceTenantMismatch) {
			t.Fatalf("foreign tenant status error = %v, want ErrInstanceTenantMismatch", err)
		}

		// Verify status
		if status.InstanceID != instanceID {
			t.Errorf("Expected instance ID '%s', got '%s'", instanceID, status.InstanceID)
		}
		// Template is no longer stored in InstanceStatus, it's derived from the group
		if status.Status == "" {
			t.Error("Status should not be empty")
		}
	})

	t.Run("DeleteInstance", func(t *testing.T) {
		instanceID, _ := puidv7.New("knc")

		// Create instance first
		req := CreateInstanceRequest{
			InstanceID: instanceID,
			Tenant:     "default",
			Group:      "test-group",
			Template:   "",
		}

		_, err := manager.CreateInstance(ctx, req)
		if err != nil {
			t.Fatalf("Failed to create instance: %v", err)
		}

		if err := manager.DeleteInstance(ctx, "other", instanceID); !errors.Is(err, ErrInstanceTenantMismatch) {
			t.Fatalf("foreign tenant deletion error = %v, want ErrInstanceTenantMismatch", err)
		}

		// Delete instance
		err = manager.DeleteInstance(ctx, "default", instanceID)
		if err != nil {
			t.Fatalf("Failed to delete instance: %v", err)
		}

		// Verify deletion was initiated (status should be deleting)
		// Note: mock provider simulates state transitions asynchronously
	})

	t.Run("GenerateInstanceID", func(t *testing.T) {
		req := CreateInstanceRequest{
			// InstanceID left empty - should be generated
			Tenant:   "default",
			Group:    "test-group",
			Template: "",
		}

		resp, err := manager.CreateInstance(ctx, req)
		if err != nil {
			t.Fatalf("Failed to create instance with generated ID: %v", err)
		}

		// Verify ID was generated and has correct prefix
		if resp.InstanceID == "" {
			t.Error("Instance ID should be generated")
		}

		// Verify it's a valid puidv7 with knc prefix
		_, err = puidv7.Decode(resp.InstanceID, "knc")
		if err != nil {
			t.Errorf("Generated ID should be valid puidv7 with knc prefix: %v", err)
		}
	})

	t.Run("InvalidTemplate", func(t *testing.T) {
		instanceID, _ := puidv7.New("knc")
		req := CreateInstanceRequest{
			InstanceID: instanceID,
			Tenant:     "default",
			Group:      "test-group",
			Template:   "nonexistent",
		}

		_, err := manager.CreateInstance(ctx, req)
		if err == nil {
			t.Error("Expected error for nonexistent template")
		}
	})
}

func TestUserdataTemplateProcessing(t *testing.T) {
	ctx := context.Background()

	// Create minimal manager for userdata testing
	secretsStore := secrets.NewMemoryStore()
	_, noncePrivateKeyPEM, err := GenerateEd25519KeyPairForTesting()
	if err != nil {
		t.Fatalf("Failed to generate nonce key: %v", err)
	}
	if err := secretsStore.Set(ctx, "registration-nonce.key", noncePrivateKeyPEM); err != nil {
		t.Fatalf("Failed to store registration nonce key: %v", err)
	}

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
				Provider: "mock",
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
				HealthAddr:       "172.16.0.1:8990",
				ElectionAddr:     "172.16.0.1:8991",
				RegistrationAddr: "172.16.0.1:8992",
				OperatorAddr:     "172.16.0.1:8993",
				AgentAddr:        "172.16.0.1:8994",
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

	// Create test database for config loader
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
		Storage:      storage.NewMock(),
		CacheStorage: storage.NewMock(),
		LocalDB:      testDB,
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create config loader: %v", err)
	}
	configLoader.SetConfig(testConfig)

	manager := &Manager{
		configLoader: configLoader,
		secretsStore: secretsStore,
		logger:       slog.Default(),
	}
	if err := manager.initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize manager: %v", err)
	}

	t.Run("ProcessSimpleTemplate", func(t *testing.T) {
		userdataConfig := &config.UserdataConfig{Content: "#!/bin/bash\necho 'Instance: {{ .Instance.ID }}'\necho 'Arch: {{ .Instance.Arch }}'"}
		templateData := UserdataTemplateData{
			Instance: InstanceData{
				ID:   "test-instance",
				Arch: "amd64",
				Type: "t3.medium",
			},
		}

		userdata, err := manager.processUserdataTemplate(userdataConfig, templateData)
		if err != nil {
			t.Fatalf("Failed to process userdata template: %v", err)
		}

		expectedContent := []string{
			"Instance: test-instance",
			"Arch: amd64",
		}

		for _, expected := range expectedContent {
			if !strings.Contains(userdata, expected) {
				t.Errorf("Expected userdata to contain '%s', got: %s", expected, userdata)
			}
		}
	})

	t.Run("ProcessTemplateWithVars", func(t *testing.T) {
		userdataConfig := &config.UserdataConfig{Content: "#!/bin/bash\necho 'RegAddr: {{ .Server.RegistrationAddr }}'\necho 'AgentAddr: {{ .Server.AgentAddr }}'\necho 'Environment: {{ .Vars.Environment }}'"}
		templateData := UserdataTemplateData{
			Server: ServerData{
				RegistrationAddr: "172.16.0.1:8992",
				AgentAddr:        "172.16.0.1:8994",
				OperatorAddr:     "172.16.0.1:8993",
			},
			Vars: map[string]string{
				"Environment": "production",
				"Version":     "1.0.0",
			},
		}

		userdata, err := manager.processUserdataTemplate(userdataConfig, templateData)
		if err != nil {
			t.Fatalf("Failed to process userdata template: %v", err)
		}

		expectedContent := []string{
			"RegAddr: 172.16.0.1:8992",
			"AgentAddr: 172.16.0.1:8994",
			"Environment: production",
		}

		for _, expected := range expectedContent {
			if !strings.Contains(userdata, expected) {
				t.Errorf("Expected userdata to contain '%s', got: %s", expected, userdata)
			}
		}
	})
}

// Helper function for testing
func GenerateEd25519KeyPairForTesting() (publicKeyPEM, privateKeyPEM []byte, err error) {
	// Generate Ed25519 key pair
	privateKey, err := keys.GenerateEd25519Key()
	if err != nil {
		return nil, nil, err
	}

	privateKeyPEM, err = keys.MarshalEd25519PrivateKey(privateKey)
	if err != nil {
		return nil, nil, err
	}

	// For testing, we only need the private key since JWT signing uses the private key
	// The public key would be extracted from it
	return []byte("mock-public-key"), privateKeyPEM, nil
}

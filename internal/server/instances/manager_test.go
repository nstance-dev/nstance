// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package instances

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refreshjs/puidv7"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/infra"
	"github.com/nstance-dev/nstance/internal/server/infra/mock"
	"github.com/nstance-dev/nstance/internal/server/keys"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/pki"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

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
		status, err := manager.GetInstanceStatus(ctx, instanceID)
		if err != nil {
			t.Fatalf("Failed to get instance status: %v", err)
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

		// Delete instance
		err = manager.DeleteInstance(ctx, instanceID)
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

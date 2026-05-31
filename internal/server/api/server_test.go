// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/api/agent"
	"github.com/nstance-dev/nstance/internal/server/api/registration"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/keys"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/pki"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// createTestServices creates real service implementations for testing
func createTestServices(t *testing.T) (*registration.Service, *agent.Service, []byte, []byte) {
	t.Helper()

	ctx := context.Background()

	// Generate test CA
	caCertPEM, caPrivateKeyPEM, err := pki.GenerateTestCA()
	if err != nil {
		t.Fatalf("Failed to generate test CA: %v", err)
	}

	// Create temporary database
	tempFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatalf("Failed to create temp database file: %v", err)
	}
	defer func() {
		err := tempFile.Close()
		if err != nil {
			t.Fatalf("Failed to close temp database file: %v", err)
		}
	}()
	t.Cleanup(func() {
		err := os.Remove(tempFile.Name())
		if err != nil {
			t.Fatalf("Failed to remove temp database file: %v", err)
		}
	})

	// Create mock secrets store with required private keys (not public certs)
	secretsStore := secrets.NewMemoryStore()
	if err := secretsStore.Set(ctx, "ca.key", caPrivateKeyPEM); err != nil {
		t.Fatalf("Failed to store CA key: %v", err)
	}

	// Generate and store registration nonce key (required by RegistrationService)
	_, noncePrivateKeyPEM, err := keys.GenerateTestEd25519KeyPair()
	if err != nil {
		t.Fatalf("Failed to generate nonce key: %v", err)
	}
	if err := secretsStore.Set(ctx, "registration-nonce.key", noncePrivateKeyPEM); err != nil {
		t.Fatalf("Failed to store registration nonce key: %v", err)
	}

	// Create local database
	localDB, err := localdb.Open(tempFile.Name())
	if err != nil {
		t.Fatalf("Failed to open local database: %v", err)
	}
	t.Cleanup(func() { _ = localDB.Close() })

	// Create config loader
	mainStorage := storage.NewMock()
	cacheStorage := storage.NewMock()

	// Store CA certificate in S3 storage (cluster-wide, public)
	if err := mainStorage.Put(ctx, "ca.crt", caCertPEM); err != nil {
		t.Fatalf("Failed to store CA certificate in S3: %v", err)
	}

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

	// Create a valid test config
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
	configLoader.SetConfig(testConfig)

	// Create registration service
	regService, err := registration.New(registration.Options{
		ShardStorage:    mainStorage,
		ClusterStorage:  mainStorage, // Use same storage for tests
		SecretsStore:    secretsStore,
		LocalDB:         localDB,
		ConfigLoader:    configLoader,
		IsShardLeader:   func() bool { return true }, // Assume leader for tests
		IsClusterLeader: func() bool { return true }, // Assume leader for tests
		Logger:          slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create registration service: %v", err)
	}

	// Create agent service
	agentService, err := agent.New(agent.Options{
		Storage:      mainStorage,
		ConfigLoader: configLoader,
		LocalDB:      localDB,
		SecretsStore: secretsStore,
		CACertPEM:    caCertPEM,
		CAKeyPEM:     caPrivateKeyPEM,
		Shard:        "test-shard",
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create agent service: %v", err)
	}

	return regService, agentService, caCertPEM, caPrivateKeyPEM
}

func TestServer(t *testing.T) {
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
				RegistrationAddr: "127.0.0.1:0",
				OperatorAddr:     "127.0.0.1:0",
				AgentAddr:        "127.0.0.1:0",
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

	ctx := context.Background()
	t.Run("ServerStartStop", func(t *testing.T) {
		// Create real services for testing
		regService, agentService, caCertPEM, caKeyPEM := createTestServices(t)

		// Generate server certificate for TLS
		serverCertPEM, serverKeyPEM, err := pki.GenerateServerCertificate(caCertPEM, caKeyPEM, testConfig.Shard.Bind.RegistrationAddr)
		if err != nil {
			t.Fatalf("Failed to generate server certificate: %v", err)
		}

		// Create server with real services
		server, err := api.NewServer(api.ServerOptions{
			Config:              testConfig,
			Logger:              slog.Default(),
			RegistrationService: regService,
			AgentService:        agentService,
			OperatorService:     &api.StubOperatorService{},
			CACertPEM:           caCertPEM,
			ServerCertPEM:       serverCertPEM,
			ServerKeyPEM:        serverKeyPEM,
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}

		// Start server
		err = server.Start(ctx)
		if err != nil {
			t.Fatalf("Failed to start server: %v", err)
		}

		// Verify server is started
		if !server.IsStarted() {
			t.Error("Server should be started")
		}

		// Stop server
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err = server.Stop(stopCtx)
		if err != nil {
			t.Fatalf("Failed to stop server: %v", err)
		}

		// Verify server is stopped
		if server.IsStarted() {
			t.Error("Server should be stopped")
		}
	})

	t.Run("ClientConnections", func(t *testing.T) {
		// Create real services for testing
		regService, agentService, caCertPEM, caKeyPEM := createTestServices(t)

		// Generate server certificate for TLS
		serverCertPEM, serverKeyPEM, err := pki.GenerateServerCertificate(caCertPEM, caKeyPEM, testConfig.Shard.Bind.RegistrationAddr)
		if err != nil {
			t.Fatalf("Failed to generate server certificate: %v", err)
		}

		// Create server
		server, err := api.NewServer(api.ServerOptions{
			Config:              testConfig,
			Logger:              slog.Default(),
			RegistrationService: regService,
			AgentService:        agentService,
			OperatorService:     &api.StubOperatorService{},
			CACertPEM:           caCertPEM,
			ServerCertPEM:       serverCertPEM,
			ServerKeyPEM:        serverKeyPEM,
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}

		// Start server
		err = server.Start(ctx)
		if err != nil {
			t.Fatalf("Failed to start server: %v", err)
		}
		defer func() {
			stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_ = server.Stop(stopCtx)
		}()

		// Get actual bind addresses (since we used port 0)
		regAddr := server.RegistrationListener.Addr().String()
		agentAddr := server.AgentListener.Addr().String()
		operatorAddr := server.OperatorListener.Addr().String()

		// Test registration service connection
		t.Run("RegistrationService", func(t *testing.T) {
			conn, err := grpc.NewClient(regAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatalf("Failed to connect to registration service: %v", err)
			}
			defer func() { _ = conn.Close() }()

			client := proto.NewRegistrationServiceClient(conn)

			// Test RegisterAgent (should return unimplemented)
			_, err = client.RegisterAgent(ctx, &proto.RegisterClientRequest{
				RegistrationNonceJwt: "test-jwt",
				PublicKeyPem:         []byte("test-key"),
			})
			if err == nil {
				t.Error("Expected unimplemented error")
			}
		})

		// Test agent service connection
		t.Run("AgentService", func(t *testing.T) {
			conn, err := grpc.NewClient(agentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatalf("Failed to connect to agent service: %v", err)
			}
			defer func() { _ = conn.Close() }()

			client := proto.NewAgentServiceClient(conn)

			// Test SubmitHealthReport (should fail with authentication error - no client cert)
			stream, err := client.SubmitHealthReport(ctx)
			if err == nil {
				// Try to send a report
				err = stream.Send(&proto.HealthReportRequest{
					InstanceId: "test-instance",
				})
				if err == nil {
					t.Error("Expected authentication error")
				}
			}
		})

		// Test operator service connection
		t.Run("OperatorService", func(t *testing.T) {
			conn, err := grpc.NewClient(operatorAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatalf("Failed to connect to operator service: %v", err)
			}
			defer func() { _ = conn.Close() }()

			client := proto.NewOperatorServiceClient(conn)

			// Test GetConfigStatus (should return unimplemented)
			_, err = client.GetConfigStatus(ctx, &emptypb.Empty{})
			if err == nil {
				t.Error("Expected unimplemented error")
			}
		})
	})
}

func TestServerValidation(t *testing.T) {
	t.Run("RequiredConfig", func(t *testing.T) {
		// Create real services for testing
		regService, agentService, _, _ := createTestServices(t)

		_, err := api.NewServer(api.ServerOptions{
			RegistrationService: regService,
			AgentService:        agentService,
			OperatorService:     &api.StubOperatorService{},
		})
		if err == nil {
			t.Error("Expected error when config is not provided")
		}
	})

	t.Run("RequiredServices", func(t *testing.T) {
		testConfig := &config.Config{
			Cluster: config.ClusterConfig{
				ID: "example-cluster",
			},
			Shard: config.ShardConfig{
				Bind: config.BindConfig{
					HealthAddr:       "127.0.0.1:8990",
					ElectionAddr:     "127.0.0.1:8991",
					RegistrationAddr: "127.0.0.1:0",
					OperatorAddr:     "127.0.0.1:0",
					AgentAddr:        "127.0.0.1:0",
				},
				Advertise: config.AdvertiseConfig{
					HealthAddr:       "127.0.0.1:8990",
					ElectionAddr:     "127.0.0.1:8991",
					RegistrationAddr: "127.0.0.1:8992",
					OperatorAddr:     "127.0.0.1:8993",
					AgentAddr:        "127.0.0.1:8994",
				},
			},
		}

		// Create real services for testing
		regService, agentService, _, _ := createTestServices(t)

		// Missing registration service
		_, err := api.NewServer(api.ServerOptions{
			Config:       testConfig,
			AgentService: agentService,
		})
		if err == nil {
			t.Error("Expected error when registration service is not provided")
		}

		// Missing agent service
		_, err = api.NewServer(api.ServerOptions{
			Config:              testConfig,
			RegistrationService: regService,
		})
		if err == nil {
			t.Error("Expected error when agent service is not provided")
		}
	})
}

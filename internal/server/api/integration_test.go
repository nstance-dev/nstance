// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

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

func TestRegistrationServiceIntegration(t *testing.T) {
	ctx := context.Background()

	// Generate test CA and keys
	caCertPEM, caPrivateKeyPEM, err := pki.GenerateTestCA()
	if err != nil {
		t.Fatalf("Failed to generate test CA: %v", err)
	}

	_, noncePrivateKeyPEM, err := keys.GenerateTestEd25519KeyPair()
	if err != nil {
		t.Fatalf("Failed to generate nonce key: %v", err)
	}

	noncePrivateKey, err := keys.ParseEd25519PrivateKey(noncePrivateKeyPEM)
	if err != nil {
		t.Fatalf("Failed to parse nonce private key: %v", err)
	}

	// Create secrets store with test data (only private keys, not public certs)
	secretsStore := secrets.NewMemoryStore()
	if err := secretsStore.Set(ctx, "ca.key", caPrivateKeyPEM); err != nil {
		t.Fatalf("Failed to store CA key: %v", err)
	}
	if err := secretsStore.Set(ctx, "registration-nonce.key", noncePrivateKeyPEM); err != nil {
		t.Fatalf("Failed to store registration nonce key: %v", err)
	}

	// Create temporary SQLite database
	tempFile := "/tmp/nstance-integration-test.db"
	defer func() { _ = os.Remove(tempFile) }()

	localDB, err := localdb.Open(tempFile)
	if err != nil {
		t.Fatalf("Failed to open local database: %v", err)
	}
	defer func() { _ = localDB.Close() }()

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

	// Load test config
	configLoader.SetConfig(testConfig)

	// Create real registration service
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

	// Generate server certificate for TLS
	serverCertPEM, serverKeyPEM, err := pki.GenerateServerCertificate(caCertPEM, caPrivateKeyPEM, testConfig.Shard.Bind.RegistrationAddr)
	if err != nil {
		t.Fatalf("Failed to generate server certificate: %v", err)
	}

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

	// Get actual registration service address
	regAddr := server.RegistrationListener.Addr().String()

	// Create TLS credentials for client (using CA cert to verify server)
	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		t.Fatalf("Failed to decode CA certificate")
	}
	caCertParsed, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse CA certificate: %v", err)
	}
	certPool := x509.NewCertPool()
	certPool.AddCert(caCertParsed)

	tlsConfig := &tls.Config{
		RootCAs:    certPool,
		ServerName: "nstance-server",
		MinVersion: tls.VersionTLS13,
	}
	creds := credentials.NewTLS(tlsConfig)

	// Create gRPC client
	conn, err := grpc.NewClient(regAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatalf("Failed to connect to registration service: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := proto.NewRegistrationServiceClient(conn)

	t.Run("RegisterAgentEndToEnd", func(t *testing.T) {
		// Generate agent key pair
		agentPublicKeyPEM, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatalf("Failed to generate agent key: %v", err)
		}

		// Generate valid JWT for agent with matching cluster_id and shard from testConfig
		instanceID := "knc0000000001r010000000000000"
		jwt, err := api.GenerateTestJWTWithClaims(noncePrivateKey, "agent", instanceID,
			"example-cluster", "test-shard", "test-group", "default", false, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to generate JWT: %v", err)
		}

		// Create instance record in localDB (simulating server spawning the instance)
		now := time.Now().UTC()
		instance := &localdb.Instance{
			ID:        instanceID,
			Tenant:    "default",
			Nonce:     jwt,
			IssuedAt:  &now,
			CreatedAt: now,
		}
		if err := localDB.CreateInstance(instance); err != nil {
			t.Fatalf("Failed to create instance record: %v", err)
		}

		// Call RegisterAgent via gRPC
		req := &proto.RegisterClientRequest{
			RegistrationNonceJwt: jwt,
			PublicKeyPem:         agentPublicKeyPEM,
		}

		resp, err := client.RegisterAgent(ctx, req)
		if err != nil {
			t.Fatalf("Failed to register agent via gRPC: %v", err)
		}

		// Verify response
		if resp.ClientCertificatePem == nil {
			t.Error("Client certificate should not be nil")
		}
		if resp.ExpiresAt == nil {
			t.Error("Expires at should not be nil")
		}

		// Verify certificate is valid and not expired
		if resp.ExpiresAt.AsTime().Before(time.Now()) {
			t.Error("Certificate should not be expired")
		}

		// Try to register again (should fail with AlreadyExists)
		_, err = client.RegisterAgent(ctx, req)
		if err == nil {
			t.Error("Expected error when registering same agent twice")
		}
	})

	t.Run("RegisterOperatorEndToEnd", func(t *testing.T) {
		// Generate operator key pair
		operatorPublicKeyPEM, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatalf("Failed to generate operator key: %v", err)
		}

		// Generate valid JWT for operator
		clusterID := "example-cluster"
		jwt, err := api.GenerateTestJWTWithClaims(noncePrivateKey, "operator", clusterID, clusterID, "test-shard", "", "default", false, 30*time.Minute)
		if err != nil {
			t.Fatalf("Failed to generate JWT: %v", err)
		}

		// Call RegisterOperator via gRPC
		req := &proto.RegisterClientRequest{
			RegistrationNonceJwt: jwt,
			PublicKeyPem:         operatorPublicKeyPEM,
		}

		resp, err := client.RegisterOperator(ctx, req)
		if err != nil {
			t.Fatalf("Failed to register operator via gRPC: %v", err)
		}

		// Verify response
		if resp.ClientCertificatePem == nil {
			t.Error("Client certificate should not be nil")
		}
		if resp.ExpiresAt == nil {
			t.Error("Expires at should not be nil")
		}

		// Retrying after a lost response is safe with the same key.
		if _, err = client.RegisterOperator(ctx, req); err != nil {
			t.Fatalf("same-key replay failed: %v", err)
		}
		wrongKey, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatal(err)
		}
		req.PublicKeyPem = wrongKey
		if _, err = client.RegisterOperator(ctx, req); err == nil {
			t.Error("different-key replay succeeded")
		}
	})

	t.Run("WrongEndpointKind", func(t *testing.T) {
		// Generate key pair
		publicKeyPEM, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatalf("Failed to generate key: %v", err)
		}

		// Generate agent JWT but try to use operator endpoint
		instanceID := "knc0000000001r010000000000001"
		jwt, err := api.GenerateTestJWT(noncePrivateKey, "agent", instanceID, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to generate JWT: %v", err)
		}

		req := &proto.RegisterClientRequest{
			RegistrationNonceJwt: jwt,
			PublicKeyPem:         publicKeyPEM,
		}

		// Should fail when using agent JWT with operator endpoint
		_, err = client.RegisterOperator(ctx, req)
		if err == nil {
			t.Error("Expected error when using agent JWT with operator endpoint")
		}
	})
}

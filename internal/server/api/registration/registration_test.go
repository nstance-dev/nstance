// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/keys"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/pki"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

func TestService(t *testing.T) {
	ctx := context.Background()

	// Generate test CA and keys
	caCertPEM, caPrivateKeyPEM, err := pki.GenerateTestCA()
	if err != nil {
		t.Fatalf("Failed to generate test CA: %v", err)
	}

	// Generate registration nonce key
	_, noncePrivateKeyPEM, err := keys.GenerateTestEd25519KeyPair()
	if err != nil {
		t.Fatalf("Failed to generate nonce key: %v", err)
	}

	noncePrivateKey, err := keys.ParseEd25519PrivateKey(noncePrivateKeyPEM)
	if err != nil {
		t.Fatalf("Failed to parse nonce private key: %v", err)
	}

	// Create mock storage and secrets store
	mockStorage := storage.NewMock()
	if err := mockStorage.Put(ctx, "ca.crt", caCertPEM); err != nil {
		t.Fatalf("Failed to store CA certificate: %v", err)
	}

	secretsStore := secrets.NewMemoryStore()
	if err := secretsStore.Set(ctx, "ca.key", caPrivateKeyPEM); err != nil {
		t.Fatalf("Failed to store CA key: %v", err)
	}
	if err := secretsStore.Set(ctx, "registration-nonce.key", noncePrivateKeyPEM); err != nil {
		t.Fatalf("Failed to store registration nonce key: %v", err)
	}

	// Create temporary SQLite database
	tempFile := "/tmp/nstance-registration-test.db"
	defer func() { _ = os.Remove(tempFile) }()

	localDB, err := localdb.Open(tempFile)
	if err != nil {
		t.Fatalf("Failed to open local database: %v", err)
	}
	defer func() { _ = localDB.Close() }()

	// Create test config loader with minimal config
	configStorage := storage.NewMock()
	configCacheStorage := storage.NewMock()
	configLoader, err := config.NewLoader(config.LoaderOptions{
		Storage:      configStorage,
		CacheStorage: configCacheStorage,
		LocalDB:      localDB,
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create config loader: %v", err)
	}
	testConfig := &config.Config{
		Cluster: config.ClusterConfig{
			ID: "test-cluster",
		},
		Shard: config.ShardConfig{
			ID: "test-shard",
		},
	}
	configLoader.SetConfig(testConfig)

	// Create registration service
	service, err := New(Options{
		ShardStorage:    mockStorage,
		ClusterStorage:  mockStorage, // Use same mock storage for tests
		SecretsStore:    secretsStore,
		LocalDB:         localDB,
		ConfigLoader:    configLoader,
		IsShardLeader:   func() bool { return true }, // Assume shard leader for tests
		IsClusterLeader: func() bool { return true }, // Assume cluster leader for tests
		Logger:          slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create registration service: %v", err)
	}

	t.Run("RegisterAgent", func(t *testing.T) {
		// Generate test agent key
		agentPublicKeyPEM, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatalf("Failed to generate agent key: %v", err)
		}

		// Generate valid JWT for agent
		instanceID := "knc0000000001r010000000000000"
		jwt, err := api.GenerateTestJWT(noncePrivateKey, "agent", instanceID, 5*time.Minute)
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

		// Register agent
		req := &proto.RegisterClientRequest{
			RegistrationNonceJwt: jwt,
			PublicKeyPem:         agentPublicKeyPEM,
		}

		resp, err := service.RegisterAgent(ctx, req)
		if err != nil {
			t.Fatalf("Failed to register agent: %v", err)
		}

		// Verify response
		if resp.ClientCertificatePem == nil {
			t.Error("Client certificate should not be nil")
		}
		if resp.ExpiresAt == nil {
			t.Error("Expires at should not be nil")
		}

		// Verify certificate expires in the future
		if resp.ExpiresAt.AsTime().Before(time.Now()) {
			t.Error("Certificate should not be expired")
		}

		// Try to register the same agent again (should fail)
		_, err = service.RegisterAgent(ctx, req)
		if err == nil {
			t.Error("Expected error when registering same agent twice")
		}
	})

	t.Run("RegisterOperator", func(t *testing.T) {
		// Generate test operator key
		operatorPublicKeyPEM, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatalf("Failed to generate operator key: %v", err)
		}

		// Generate valid JWT for operator
		clusterID := "test-cluster"
		jwt, err := api.GenerateTestJWT(noncePrivateKey, "operator", clusterID, 30*time.Minute)
		if err != nil {
			t.Fatalf("Failed to generate JWT: %v", err)
		}

		// Register operator
		req := &proto.RegisterClientRequest{
			RegistrationNonceJwt: jwt,
			PublicKeyPem:         operatorPublicKeyPEM,
		}

		resp, err := service.RegisterOperator(ctx, req)
		if err != nil {
			t.Fatalf("Failed to register operator: %v", err)
		}

		// Verify response
		if resp.ClientCertificatePem == nil {
			t.Error("Client certificate should not be nil")
		}
		if resp.ExpiresAt == nil {
			t.Error("Expires at should not be nil")
		}

		// A retry after a lost response succeeds for the persisted key.
		if _, err = service.RegisterOperator(ctx, req); err != nil {
			t.Fatalf("same-key registration replay failed: %v", err)
		}
		wrongKey, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatal(err)
		}
		req.PublicKeyPem = wrongKey
		if _, err = service.RegisterOperator(ctx, req); err == nil {
			t.Error("different-key registration replay succeeded")
		}

		// The durable binding survives local database and leader replacement.
		otherDB, err := localdb.Open(t.TempDir() + "/nstance.db")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = otherDB.Close() }()
		otherService, err := New(Options{
			ShardStorage:    mockStorage,
			ClusterStorage:  mockStorage,
			SecretsStore:    secretsStore,
			LocalDB:         otherDB,
			ConfigLoader:    configLoader,
			IsShardLeader:   func() bool { return true },
			IsClusterLeader: func() bool { return true },
			Logger:          slog.Default(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = otherService.RegisterOperator(ctx, req); err == nil {
			t.Error("different-key registration replay succeeded after leader replacement")
		}
		req.PublicKeyPem = operatorPublicKeyPEM
		if _, err = otherService.RegisterOperator(ctx, req); err != nil {
			t.Fatalf("same-key registration replay failed after leader replacement: %v", err)
		}
	})

	t.Run("InvalidJWT", func(t *testing.T) {
		agentPublicKeyPEM, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatalf("Failed to generate agent key: %v", err)
		}

		// Test with invalid JWT
		req := &proto.RegisterClientRequest{
			RegistrationNonceJwt: "invalid.jwt.token",
			PublicKeyPem:         agentPublicKeyPEM,
		}

		_, err = service.RegisterAgent(ctx, req)
		if err == nil {
			t.Error("Expected error for invalid JWT")
		}
	})

	t.Run("WrongKindJWT", func(t *testing.T) {
		agentPublicKeyPEM, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatalf("Failed to generate agent key: %v", err)
		}

		// Generate JWT with wrong kind (operator JWT used for agent registration)
		instanceID := "knc0000000001r010000000000000"
		jwt, err := api.GenerateTestJWT(noncePrivateKey, "operator", instanceID, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to generate JWT: %v", err)
		}

		req := &proto.RegisterClientRequest{
			RegistrationNonceJwt: jwt,
			PublicKeyPem:         agentPublicKeyPEM,
		}

		_, err = service.RegisterAgent(ctx, req)
		if err == nil {
			t.Error("Expected error for wrong JWT kind")
		}
	})

	t.Run("ExpiredJWT", func(t *testing.T) {
		agentPublicKeyPEM, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatalf("Failed to generate agent key: %v", err)
		}

		// Generate expired JWT
		instanceID := "knc0000000001r010000000000001"
		jwt, err := api.GenerateTestJWT(noncePrivateKey, "agent", instanceID, -time.Hour) // Expired 1 hour ago
		if err != nil {
			t.Fatalf("Failed to generate JWT: %v", err)
		}

		req := &proto.RegisterClientRequest{
			RegistrationNonceJwt: jwt,
			PublicKeyPem:         agentPublicKeyPEM,
		}

		_, err = service.RegisterAgent(ctx, req)
		if err == nil {
			t.Error("Expected error for expired JWT")
		}
	})
}

func TestCertificateGenerator(t *testing.T) {
	// Generate test CA
	caCertPEM, caPrivateKeyPEM, err := pki.GenerateTestCA()
	if err != nil {
		t.Fatalf("Failed to generate test CA: %v", err)
	}

	t.Run("GenerateClientCert", func(t *testing.T) {
		// Generate client key pair
		clientPublicKeyPEM, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatalf("Failed to generate client key: %v", err)
		}

		// Generate certificate
		certPEM, expiresAt, err := pki.GenerateClientCertificate(caCertPEM, caPrivateKeyPEM, clientPublicKeyPEM, "test-client", "agent", "default", 24)
		if err != nil {
			t.Fatalf("Failed to generate certificate: %v", err)
		}

		// Verify certificate
		if certPEM == nil {
			t.Error("Certificate PEM should not be nil")
		}
		if expiresAt.Before(time.Now()) {
			t.Error("Certificate should not be expired")
		}
		if expiresAt.After(time.Now().Add(25 * time.Hour)) {
			t.Error("Certificate expiry seems too far in the future")
		}
	})
}

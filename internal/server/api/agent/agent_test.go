// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/puidv7/puidv7-go"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/keys"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/pki"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

func StringPtr(s string) *string {
	return &s
}

func TestService(t *testing.T) {
	ctx := context.Background()

	// Generate proper instance ID using puidv7 with "tst" prefix
	instanceID, err := puidv7.New("tst")
	if err != nil {
		t.Fatalf("Failed to generate instance ID: %v", err)
	}

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
		Certificates: map[string]config.CertConfig{
			"kubelet.client": {
				Kind:         "client",
				CN:           StringPtr("system:node:{{ .Instance.ID }}"),
				Organization: []string{"system:nodes"},
				DNS: []string{
					"localhost",
					"{{ .Instance.ID }}",
				},
				IP: []string{
					"127.0.0.1",
				},
				TTL: 8760,
			},
		},
		Templates: map[string]config.TemplateConfig{
			"test": {
				Kind: "tst",
				Arch: "amd64",
			},
		},
		Groups: map[string]map[string]config.GroupConfig{
			"default": {
				"test-group": {
					Template: "test",
					Size:     config.IntPtr(1),
				},
			},
		},
	}
	testConfig.SetDefaults()

	// Generate test CA
	caCertPEM, caPrivateKeyPEM, err := pki.GenerateTestCA()
	if err != nil {
		t.Fatalf("Failed to generate test CA: %v", err)
	}

	// Create secrets store with test data
	secretsStore := secrets.NewMemoryStore()
	if err := secretsStore.Set(ctx, "ca.crt", caCertPEM); err != nil {
		t.Fatalf("Failed to store CA certificate: %v", err)
	}
	if err := secretsStore.Set(ctx, "ca.key", caPrivateKeyPEM); err != nil {
		t.Fatalf("Failed to store CA key: %v", err)
	}

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

	// Load config into loader
	configLoader.SetConfig(testConfig)

	// Verify config was loaded properly
	currentConfig := configLoader.GetCurrent()
	if currentConfig == nil {
		t.Fatalf("Config loader returned nil config")
	}
	if len(currentConfig.Templates) == 0 {
		t.Fatalf("Config loader has no templates")
	}
	if _, exists := currentConfig.Templates["test"]; !exists {
		t.Fatalf("Template 'test' not found in config loader")
	}

	// Create temporary SQLite database
	tempFile := "/tmp/nstance-agent-test.db"
	defer func() { _ = os.Remove(tempFile) }()

	localDB, err := localdb.Open(tempFile)
	if err != nil {
		t.Fatalf("Failed to open local database: %v", err)
	}
	defer func() { _ = localDB.Close() }()

	// Create CA certificate and key for testing
	caCertPEM, caKeyPEM, err := pki.GenerateTestCA()
	if err != nil {
		t.Fatalf("Failed to generate test CA: %v", err)
	}

	// Create agent service
	agentService, err := New(Options{
		Storage:      mainStorage,
		ConfigLoader: configLoader,
		LocalDB:      localDB,
		SecretsStore: secretsStore,
		CACertPEM:    caCertPEM,
		CAKeyPEM:     caKeyPEM,
		Shard:        "test-shard",
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("Failed to create agent service: %v", err)
	}

	// Create instance record in SQLite first (simulate registration) - used by all tests
	now := time.Now().UTC()
	hostname := "test-host"
	fqdn := "test-host.test-cluster.cluster.cool"
	ip4 := "172.16.0.100"
	ip6 := "2001:db8::1"

	// Generate unique nonce to avoid UNIQUE constraint failures across test runs
	testNonce, err := puidv7.New("nce")
	if err != nil {
		t.Fatalf("Failed to generate test nonce: %v", err)
	}

	instanceRecord := &localdb.Instance{
		ID:           instanceID,
		Tenant:       "default",
		Group:        "test-group", // Template is derived from group config
		Nonce:        testNonce,
		IssuedAt:     &now,
		RegisteredAt: &now,
		Hostname:     &hostname,
		FQDN:         &fqdn,
		IP4:          &ip4,
		IP6:          &ip6,
	}
	if err := localDB.CreateInstance(instanceRecord); err != nil {
		t.Fatalf("Failed to create instance record: %v", err)
	}

	t.Run("SubmitHealthReport", func(t *testing.T) {

		// Create context with client info
		clientInfo := &api.ClientInfo{
			ClientID: instanceID,
			Role:     "agent",
		}
		ctxWithClient := context.WithValue(ctx, api.ClientInfoKey, clientInfo)

		// Create health report
		req := &proto.HealthReportRequest{
			InstanceId: instanceID,
			Timestamp:  timestamppb.New(time.Now().UTC()),
			Count:      1,
			Uptime:     "72h30m15s",
			Files: map[string]*proto.FileStatus{
				"ca.crt": {
					Status: &proto.FileStatus_LastModified{
						LastModified: timestamppb.New(time.Now().UTC().Add(-time.Hour)),
					},
				},
				"kubelet.key": {
					Status: &proto.FileStatus_Error{
						Error: "permission denied",
					},
				},
			},
		}

		// Create mock stream
		stream := &mockSubmitHealthReportStream{
			ctx:      ctxWithClient,
			requests: []*proto.HealthReportRequest{req},
		}

		// Submit health report via stream
		err := agentService.SubmitHealthReport(stream)
		if err != nil {
			t.Fatalf("Failed to submit health report: %v", err)
		}

		// Verify health record was stored in SQLite
		instance, err := localDB.GetInstance(instanceID)
		if err != nil {
			t.Fatalf("Failed to get instance from SQLite: %v", err)
		}

		if len(instance.Health) == 0 {
			t.Error("Health record should not be empty")
		}
	})

	t.Run("SubmitHealthReportWrongInstance", func(t *testing.T) {
		// Create context with different client info
		clientInfo := &api.ClientInfo{
			ClientID: "different-instance",
			Role:     "agent",
		}
		ctxWithClient := context.WithValue(ctx, api.ClientInfoKey, clientInfo)

		// Create health report with mismatched instance ID
		req := &proto.HealthReportRequest{
			InstanceId: instanceID, // Different from authenticated client
			Timestamp:  timestamppb.New(time.Now().UTC()),
			Count:      1,
			Uptime:     "1h",
			Files:      map[string]*proto.FileStatus{},
		}

		// Create mock stream
		stream := &mockSubmitHealthReportStream{
			ctx:      ctxWithClient,
			requests: []*proto.HealthReportRequest{req},
		}

		// Should fail due to instance ID mismatch
		err := agentService.SubmitHealthReport(stream)
		if err == nil {
			t.Error("Expected error for instance ID mismatch")
		}
	})

	t.Run("SubmitPublicKeys", func(t *testing.T) {
		// Create context with client info
		clientInfo := &api.ClientInfo{
			ClientID: instanceID,
			Role:     "agent",
		}
		ctxWithClient := context.WithValue(ctx, api.ClientInfoKey, clientInfo)

		// Generate test public key (using existing agent pattern - base64 DER)
		publicKeyPEM, _, err := keys.GenerateTestEd25519KeyPair()
		if err != nil {
			t.Fatalf("Failed to generate test key: %v", err)
		}

		// Parse and convert to DER format (simulating agent behavior)
		block, _ := pem.Decode(publicKeyPEM)
		if block == nil {
			t.Fatalf("Failed to decode test public key")
		}
		publicKeyBase64 := base64.StdEncoding.EncodeToString(block.Bytes)

		// Create public keys request
		req := &proto.PublicKeysRequest{
			InstanceId: instanceID,
			Keys: []*proto.PublicKeySubmission{
				{
					Filename:     "kubelet.client",
					PublicKeyPem: []byte(publicKeyBase64),
				},
			},
		}

		// Submit public keys
		if _, err := agentService.SubmitPublicKeys(ctxWithClient, req); err != nil {
			t.Fatalf("Failed to submit public keys: %v", err)
		}

		// Verify no certificates are queued immediately (they're generated during health reports)
		pendingFiles := agentService.getPendingFiles(instanceID)
		if len(pendingFiles) != 0 {
			t.Errorf("Expected 0 pending files (certificates generated during health reports), got %d", len(pendingFiles))
		}

		// Verify public key was stored in database
		storedKey, err := localDB.GetPublicKeyByFilename(instanceID, "kubelet.client")
		if err != nil {
			t.Fatalf("Failed to get public key: %v", err)
		}
		if storedKey == nil {
			t.Fatal("Expected stored public key")
		}
		if storedKey.Filename != "kubelet.client" {
			t.Errorf("Expected filename 'kubelet.client', got '%s'", storedKey.Filename)
		}
	})

	t.Run("ReceiveFiles", func(t *testing.T) {
		// Clear any existing pending files first
		agentService.clearPendingFiles(instanceID)

		// Queue some test files
		agentService.QueueFile(instanceID, "test.crt", []byte("test certificate"))
		agentService.QueueFile(instanceID, "config.yaml", []byte("test config"))

		// Create a stream client with cancellable context
		stream := newMockReceiveFilesStream(instanceID)

		// Run ReceiveFiles in goroutine since it blocks until context is cancelled
		done := make(chan error, 1)
		go func() {
			done <- agentService.ReceiveFiles(&emptypb.Empty{}, stream)
		}()

		// Give it time to send files, then cancel
		time.Sleep(50 * time.Millisecond)
		stream.cancel()

		// Wait for completion
		err := <-done
		if err != nil {
			t.Fatalf("ReceiveFiles failed: %v", err)
		}

		// Verify files were streamed
		if len(stream.sentFiles) != 2 {
			t.Errorf("Expected 2 files to be streamed, got %d", len(stream.sentFiles))
		}

		// Verify pending files were cleared
		pendingFiles := agentService.getPendingFiles(instanceID)
		if len(pendingFiles) != 0 {
			t.Errorf("Expected 0 pending files after streaming, got %d", len(pendingFiles))
		}

		// Check file contents
		foundTestCrt := false
		foundConfigYaml := false
		for _, file := range stream.sentFiles {
			if file.Filename == "test.crt" && string(file.Content) == "test certificate" {
				foundTestCrt = true
			}
			if file.Filename == "config.yaml" && string(file.Content) == "test config" {
				foundConfigYaml = true
			}
		}
		if !foundTestCrt {
			t.Error("Expected to find test.crt file in stream")
		}
		if !foundConfigYaml {
			t.Error("Expected to find config.yaml file in stream")
		}
	})

	t.Run("QueueFileReplacesPendingFile", func(t *testing.T) {
		agentService.clearPendingFiles(instanceID)

		agentService.QueueFile(instanceID, "test.crt", []byte("old certificate"))
		agentService.QueueFile(instanceID, "test.crt", []byte("new certificate"))

		pendingFiles := agentService.getPendingFiles(instanceID)
		if len(pendingFiles) != 1 {
			t.Fatalf("Expected 1 pending file, got %d", len(pendingFiles))
		}
		if pendingFiles[0].Filename != "test.crt" {
			t.Fatalf("Expected pending file test.crt, got %s", pendingFiles[0].Filename)
		}
		if string(pendingFiles[0].Content) != "new certificate" {
			t.Fatalf("Expected replacement content, got %q", string(pendingFiles[0].Content))
		}
	})

	t.Run("ReceiveKeyRequests", func(t *testing.T) {
		// Queue some test key requests
		agentService.queueKeyRequest(instanceID, []string{"kubelet.server", "kubelet.client"})

		// Create a stream client with cancellable context
		stream := newMockReceiveKeyRequestsStream(instanceID)

		// Run ReceiveKeyRequests in goroutine since it blocks until context is cancelled
		done := make(chan error, 1)
		go func() {
			done <- agentService.ReceiveKeyRequests(&emptypb.Empty{}, stream)
		}()

		// Give it time to send requests, then cancel
		time.Sleep(50 * time.Millisecond)
		stream.cancel()

		// Wait for completion
		err := <-done
		if err != nil {
			t.Fatalf("ReceiveKeyRequests failed: %v", err)
		}

		// Verify key requests were streamed
		if len(stream.sentRequests) != 1 {
			t.Errorf("Expected 1 key request to be streamed, got %d", len(stream.sentRequests))
		}

		// Check key names
		if len(stream.sentRequests) > 0 {
			keyNames := stream.sentRequests[0].KeyNames
			if len(keyNames) != 2 {
				t.Errorf("Expected 2 key names, got %d", len(keyNames))
			}
		}
	})

	t.Run("ReceiveFilesPushesQueuedFilesToOpenStream", func(t *testing.T) {
		agentService.clearPendingFiles(instanceID)
		stream := newMockReceiveFilesStream(instanceID)

		done := make(chan error, 1)
		go func() {
			done <- agentService.ReceiveFiles(&emptypb.Empty{}, stream)
		}()

		defer func() {
			stream.cancel()
			if err := <-done; err != nil {
				t.Fatalf("ReceiveFiles failed: %v", err)
			}
		}()

		agentService.QueueFile(instanceID, "live.crt", []byte("live certificate"))

		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if stream.sentFileCount() == 1 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("Expected live queued file to be streamed, got %d", stream.sentFileCount())
	})

	t.Run("ReceiveKeyRequestsPushesQueuedRequestsToOpenStream", func(t *testing.T) {
		stream := newMockReceiveKeyRequestsStream(instanceID)

		done := make(chan error, 1)
		go func() {
			done <- agentService.ReceiveKeyRequests(&emptypb.Empty{}, stream)
		}()

		defer func() {
			stream.cancel()
			if err := <-done; err != nil {
				t.Fatalf("ReceiveKeyRequests failed: %v", err)
			}
		}()

		agentService.queueKeyRequest(instanceID, []string{"live.key"})

		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if stream.sentRequestCount() == 1 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("Expected live queued key request to be streamed, got %d", stream.sentRequestCount())
	})

	t.Run("SubmitPublicKeysWrongInstance", func(t *testing.T) {
		// Create context with different client info
		clientInfo := &api.ClientInfo{
			ClientID: "different-instance",
			Role:     "agent",
		}
		ctxWithClient := context.WithValue(ctx, api.ClientInfoKey, clientInfo)

		// Create public keys request with mismatched instance ID
		req := &proto.PublicKeysRequest{
			InstanceId: instanceID, // Different from authenticated client
			Keys: []*proto.PublicKeySubmission{
				{
					Filename:     "kubelet.client",
					PublicKeyPem: []byte("test-key"),
				},
			},
		}

		// Should fail due to instance ID mismatch
		_, err := agentService.SubmitPublicKeys(ctxWithClient, req)
		if err == nil {
			t.Error("Expected error for instance ID mismatch")
		}
	})
}

// mockReceiveFilesStream implements a mock for streaming files
type mockReceiveFilesStream struct {
	mu         sync.Mutex
	sentFiles  []*proto.FileTransfer
	instanceID string
	ctx        context.Context
	cancel     context.CancelFunc
}

func newMockReceiveFilesStream(instanceID string) *mockReceiveFilesStream {
	ctx, cancel := context.WithCancel(context.Background())
	clientInfo := &api.ClientInfo{
		ClientID: instanceID,
		Role:     "agent",
	}
	ctx = context.WithValue(ctx, api.ClientInfoKey, clientInfo)
	return &mockReceiveFilesStream{
		instanceID: instanceID,
		ctx:        ctx,
		cancel:     cancel,
	}
}

func (m *mockReceiveFilesStream) Send(file *proto.FileTransfer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sentFiles = append(m.sentFiles, file)
	return nil
}

func (m *mockReceiveFilesStream) sentFileCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.sentFiles)
}

func (m *mockReceiveFilesStream) Context() context.Context {
	return m.ctx
}

// Unused methods for grpc.ServerStream
func (m *mockReceiveFilesStream) SetHeader(metadata.MD) error  { return nil }
func (m *mockReceiveFilesStream) SendHeader(metadata.MD) error { return nil }
func (m *mockReceiveFilesStream) SetTrailer(metadata.MD)       {}
func (m *mockReceiveFilesStream) SendMsg(interface{}) error    { return nil }
func (m *mockReceiveFilesStream) RecvMsg(interface{}) error    { return nil }

// mockReceiveKeyRequestsStream implements a mock for streaming key requests
type mockReceiveKeyRequestsStream struct {
	mu           sync.Mutex
	sentRequests []*proto.KeyGenerationRequest
	instanceID   string
	ctx          context.Context
	cancel       context.CancelFunc
}

func newMockReceiveKeyRequestsStream(instanceID string) *mockReceiveKeyRequestsStream {
	ctx, cancel := context.WithCancel(context.Background())
	clientInfo := &api.ClientInfo{
		ClientID: instanceID,
		Role:     "agent",
	}
	ctx = context.WithValue(ctx, api.ClientInfoKey, clientInfo)
	return &mockReceiveKeyRequestsStream{
		instanceID: instanceID,
		ctx:        ctx,
		cancel:     cancel,
	}
}

func (m *mockReceiveKeyRequestsStream) Send(req *proto.KeyGenerationRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sentRequests = append(m.sentRequests, req)
	return nil
}

func (m *mockReceiveKeyRequestsStream) sentRequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.sentRequests)
}

func (m *mockReceiveKeyRequestsStream) Context() context.Context {
	return m.ctx
}

// Unused methods for grpc.ServerStream
func (m *mockReceiveKeyRequestsStream) SetHeader(metadata.MD) error  { return nil }
func (m *mockReceiveKeyRequestsStream) SendHeader(metadata.MD) error { return nil }
func (m *mockReceiveKeyRequestsStream) SetTrailer(metadata.MD)       {}
func (m *mockReceiveKeyRequestsStream) SendMsg(interface{}) error    { return nil }
func (m *mockReceiveKeyRequestsStream) RecvMsg(interface{}) error    { return nil }

// mockSubmitHealthReportStream implements a mock for streaming health reports
type mockSubmitHealthReportStream struct {
	ctx      context.Context
	requests []*proto.HealthReportRequest
	index    int
	closed   bool
}

func (m *mockSubmitHealthReportStream) Recv() (*proto.HealthReportRequest, error) {
	if m.index >= len(m.requests) {
		return nil, io.EOF
	}
	req := m.requests[m.index]
	m.index++
	return req, nil
}

func (m *mockSubmitHealthReportStream) SendAndClose(*emptypb.Empty) error {
	m.closed = true
	return nil
}

func (m *mockSubmitHealthReportStream) Context() context.Context {
	return m.ctx
}

// Unused methods for grpc.ServerStream
func (m *mockSubmitHealthReportStream) SetHeader(metadata.MD) error  { return nil }
func (m *mockSubmitHealthReportStream) SendHeader(metadata.MD) error { return nil }
func (m *mockSubmitHealthReportStream) SetTrailer(metadata.MD)       {}
func (m *mockSubmitHealthReportStream) SendMsg(interface{}) error    { return nil }
func (m *mockSubmitHealthReportStream) RecvMsg(interface{}) error    { return nil }

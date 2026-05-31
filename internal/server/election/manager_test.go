// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package election

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/podplane/s3lect"
)

// mockElector implements s3lect.Elector for testing.
type mockElector struct {
	mu       sync.RWMutex
	started  bool
	stopped  bool
	leader   bool
	peerMode bool
	peerCA   []byte
	config   *s3lect.ElectorConfig
}

func newMockElector(cfg *s3lect.ElectorConfig) *mockElector {
	return &mockElector{config: cfg}
}

func (m *mockElector) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return nil
}

func (m *mockElector) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	return nil
}

func (m *mockElector) IsLeader() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.leader
}

func (m *mockElector) SetLeader(leader bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leader = leader
}

func (m *mockElector) WaitForLeadership(ctx context.Context) error {
	return nil
}

func (m *mockElector) WaitForNextElection(ctx context.Context, since time.Time) (*s3lect.LeadershipStatus, error) {
	return &s3lect.LeadershipStatus{}, nil
}

func (m *mockElector) LeaderID() string {
	return "mock-leader"
}

func (m *mockElector) GetLeadershipStatus() *s3lect.LeadershipStatus {
	return &s3lect.LeadershipStatus{}
}

func (m *mockElector) EnablePeerMode(caCert []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peerMode = true
	m.peerCA = caCert
	return nil
}

func (m *mockElector) UpdateConfig(newConfig s3lect.ElectorConfig) error {
	return nil
}

func (m *mockElector) GetConfig() *s3lect.ElectorConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// mockFactory creates a factory that returns mock electors and tracks them.
type mockFactory struct {
	mu       sync.Mutex
	electors []*mockElector
}

func newMockFactory() *mockFactory {
	return &mockFactory{}
}

func (f *mockFactory) create(opts s3lect.S3ElectorOptions) (s3lect.Elector, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := newMockElector(opts.Config)
	f.electors = append(f.electors, m)
	return m, nil
}

func (f *mockFactory) last() *mockElector {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.electors) == 0 {
		return nil
	}
	return f.electors[len(f.electors)-1]
}

func newTestManager(factory *mockFactory) *Manager {
	m := NewManager(ManagerConfig{
		ClusterID:  "test-cluster",
		ShardID:    "us-east-1a",
		ServerID:   "server-1",
		ServerAddr: "127.0.0.1:9443",
	})
	m.electorFactory = factory.create
	return m
}

func TestStartClusterElection(t *testing.T) {
	factory := newMockFactory()
	mgr := newTestManager(factory)

	ctx := context.Background()
	err := mgr.StartClusterElection(ctx, ElectionConfig{
		FrequentInterval:   5 * time.Second,
		InfrequentInterval: 30 * time.Second,
		LeaderTimeout:      15 * time.Second,
	})
	if err != nil {
		t.Fatalf("StartClusterElection: %v", err)
	}

	mock := factory.last()
	if mock == nil {
		t.Fatal("expected mock elector to be created")
	}
	if !mock.started {
		t.Error("expected elector to be started")
	}
	if mock.config.PeerHealthPath != "/health/leadership/cluster" {
		t.Errorf("expected PeerHealthPath /health/leadership/cluster, got %s", mock.config.PeerHealthPath)
	}
	if mock.config.ServerID != "us-east-1a-server-1" {
		t.Errorf("expected ServerID us-east-1a-server-1, got %s", mock.config.ServerID)
	}

	// Starting again should fail
	err = mgr.StartClusterElection(ctx, ElectionConfig{})
	if err == nil {
		t.Error("expected error starting cluster election twice")
	}
}

func TestStartShardElection(t *testing.T) {
	factory := newMockFactory()
	mgr := newTestManager(factory)

	ctx := context.Background()
	err := mgr.StartShardElection(ctx, ElectionConfig{
		PeerMode:           true,
		CACert:             []byte("test-ca"),
		FrequentInterval:   5 * time.Second,
		InfrequentInterval: 30 * time.Second,
		LeaderTimeout:      15 * time.Second,
	})
	if err != nil {
		t.Fatalf("StartShardElection: %v", err)
	}

	mock := factory.last()
	if mock == nil {
		t.Fatal("expected mock elector to be created")
	}
	if !mock.started {
		t.Error("expected elector to be started")
	}
	if mock.config.PeerHealthPath != "/health/leadership/shard" {
		t.Errorf("expected PeerHealthPath /health/leadership/shard, got %s", mock.config.PeerHealthPath)
	}
	if mock.config.ServerID != "server-1" {
		t.Errorf("expected ServerID server-1, got %s", mock.config.ServerID)
	}
}

func TestIsClusterLeader(t *testing.T) {
	factory := newMockFactory()
	mgr := newTestManager(factory)

	// Before starting, not leader
	if mgr.IsClusterLeader() {
		t.Error("expected not cluster leader before election started")
	}

	ctx := context.Background()
	if err := mgr.StartClusterElection(ctx, ElectionConfig{}); err != nil {
		t.Fatal(err)
	}

	// After starting, not leader until mock says so
	if mgr.IsClusterLeader() {
		t.Error("expected not cluster leader initially")
	}

	factory.last().SetLeader(true)
	if !mgr.IsClusterLeader() {
		t.Error("expected cluster leader after mock set")
	}
}

func TestIsShardLeader(t *testing.T) {
	factory := newMockFactory()
	mgr := newTestManager(factory)

	if mgr.IsShardLeader() {
		t.Error("expected not shard leader before election started")
	}

	ctx := context.Background()
	if err := mgr.StartShardElection(ctx, ElectionConfig{CACert: []byte("test-ca")}); err != nil {
		t.Fatal(err)
	}

	if mgr.IsShardLeader() {
		t.Error("expected not shard leader initially")
	}

	factory.last().SetLeader(true)
	if !mgr.IsShardLeader() {
		t.Error("expected shard leader after mock set")
	}
}

func TestWaitForClusterElection(t *testing.T) {
	factory := newMockFactory()
	mgr := newTestManager(factory)

	ctx := context.Background()

	// Should fail before election started
	if err := mgr.WaitForClusterElection(ctx); err == nil {
		t.Error("expected error waiting before election started")
	}

	if err := mgr.StartClusterElection(ctx, ElectionConfig{}); err != nil {
		t.Fatal(err)
	}

	// Should succeed with mock
	if err := mgr.WaitForClusterElection(ctx); err != nil {
		t.Errorf("WaitForClusterElection: %v", err)
	}
}

func TestEnableClusterPeerMode(t *testing.T) {
	factory := newMockFactory()
	mgr := newTestManager(factory)

	ctx := context.Background()

	// Should fail before election started
	if err := mgr.EnableClusterPeerMode([]byte("ca")); err == nil {
		t.Error("expected error enabling peer mode before election started")
	}

	if err := mgr.StartClusterElection(ctx, ElectionConfig{}); err != nil {
		t.Fatal(err)
	}

	caCert := []byte("test-ca-cert")
	if err := mgr.EnableClusterPeerMode(caCert); err != nil {
		t.Fatalf("EnableClusterPeerMode: %v", err)
	}

	mock := factory.last()
	if !mock.peerMode {
		t.Error("expected peer mode to be enabled")
	}
	if string(mock.peerCA) != string(caCert) {
		t.Error("expected CA cert to match")
	}
}

func TestStop(t *testing.T) {
	factory := newMockFactory()
	mgr := newTestManager(factory)

	ctx := context.Background()

	// Start both elections
	if err := mgr.StartClusterElection(ctx, ElectionConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.StartShardElection(ctx, ElectionConfig{CACert: []byte("test-ca")}); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Verify both electors were stopped
	for i, m := range factory.electors {
		if !m.stopped {
			t.Errorf("elector %d not stopped", i)
		}
	}

	// Verify state is cleared
	if mgr.IsClusterLeader() {
		t.Error("expected not cluster leader after stop")
	}
	if mgr.IsShardLeader() {
		t.Error("expected not shard leader after stop")
	}
}

func TestOnAcquireCallback(t *testing.T) {
	factory := newMockFactory()
	mgr := newTestManager(factory)

	acquired := false
	ctx := context.Background()
	err := mgr.StartClusterElection(ctx, ElectionConfig{
		OnAcquire: func(ctx context.Context) error {
			acquired = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The OnAcquire is wrapped inside the elector's OnAcquireLeadership callback.
	// Invoke it directly via the config.
	mock := factory.last()
	if mock.config.OnAcquireLeadership == nil {
		t.Fatal("expected OnAcquireLeadership to be set")
	}
	if err := mock.config.OnAcquireLeadership(ctx); err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Error("expected OnAcquire callback to be invoked")
	}
}

func TestOnLoseCallback(t *testing.T) {
	factory := newMockFactory()
	mgr := newTestManager(factory)

	lost := false
	ctx := context.Background()
	err := mgr.StartShardElection(ctx, ElectionConfig{
		CACert: []byte("test-ca"),
		OnLose: func(ctx context.Context) error {
			lost = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	mock := factory.last()
	if mock.config.OnLoseLeadership == nil {
		t.Fatal("expected OnLoseLeadership to be set")
	}
	if err := mock.config.OnLoseLeadership(ctx); err != nil {
		t.Fatal(err)
	}
	if !lost {
		t.Error("expected OnLose callback to be invoked")
	}
}

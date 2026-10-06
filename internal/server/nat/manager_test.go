// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package nat

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/infra/provider"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// testRouteProvider records route installation requests.
type testRouteProvider struct {
	routes  []provider.NATRouteRequest
	removed []provider.NATRouteRequest
}

// EnsureNATRoute records a successful route installation.
func (p *testRouteProvider) EnsureNATRoute(_ context.Context, req provider.NATRouteRequest) error {
	p.routes = append(p.routes, req)
	return nil
}

// RemoveNATRoute records one guarded route removal.
func (p *testRouteProvider) RemoveNATRoute(_ context.Context, req provider.NATRouteRequest) error {
	p.removed = append(p.removed, req)
	return nil
}

// testInstanceCreator inserts an immediately healthy NAT instance.
type testInstanceCreator struct {
	db        *localdb.DB
	request   instances.CreateInstanceRequest
	deleted   []string
	createErr error
}

// CreateNATInstance records and inserts one healthy test instance.
func (c *testInstanceCreator) CreateNATInstance(_ context.Context, req instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error) {
	c.request = req
	if c.createErr != nil {
		return nil, c.createErr
	}
	now := time.Now().UTC()
	providerID := "provider-nat"
	if err := c.db.CreateInstance(&localdb.Instance{
		ID: req.InstanceID, Tenant: req.Tenant, Group: req.Group, SubnetID: "public", ProviderID: &providerID,
		Nonce: req.InstanceID, RegisteredAt: &now, HealthAt: &now, CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	return &instances.CreateInstanceResponse{InstanceID: req.InstanceID, ProviderInstanceID: providerID}, nil
}

// DeleteInstance records deletion and removes the local test record.
func (c *testInstanceCreator) DeleteInstance(_ context.Context, _, instanceID string) error {
	c.deleted = append(c.deleted, instanceID)
	return c.db.DeleteInstance(instanceID)
}

// TestManagerCreatesHealthyNATBeforeInstallingRoute verifies nodes cannot pass
// preparation until the dedicated instance has registered and reported health.
func TestManagerCreatesHealthyNATBeforeInstallingRoute(t *testing.T) {
	db, err := localdb.Open(filepath.Join(t.TempDir(), "nat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	loader, err := config.NewLoader(config.LoaderOptions{
		Storage: storage.NewMock(), CacheStorage: storage.NewMock(), LocalDB: db, Logger: slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	loader.SetConfig(&config.Config{
		Cluster: config.ClusterConfig{ID: "cluster"},
		Shard:   config.ShardConfig{SubnetPools: map[string][]string{"public": {"public-subnet"}}},
		Templates: map[string]config.TemplateConfig{
			"nat": {Kind: "nat", SubnetPool: "public"},
		},
		Groups: map[string]map[string]config.GroupConfig{
			"red": {"nat": {Template: "nat", InstanceType: "small"}},
		},
		NAT: map[string]config.NATConfig{
			"red": {Group: "nat", PublicAddresses: []config.PublicAddress{{IPv4: "192.0.2.1", AllocationID: "eipalloc-1"}}},
		},
	})
	assignments, err := NewAssignmentStore(storage.NewMock())
	if err != nil {
		t.Fatal(err)
	}
	routes := &testRouteProvider{}
	creator := &testInstanceCreator{db: db}
	manager, err := NewManager(ManagerOptions{
		ConfigLoader: loader, LocalDB: db, Provider: routes, Assignments: assignments, Instances: creator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.PrepareSubnet(context.Background(), "red", "instance-subnet"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("first preparation error = %v, want not ready", err)
	}
	if len(routes.routes) != 0 {
		t.Fatalf("route installed before readiness: routes=%d request=%#v", len(routes.routes), creator.request)
	}
	if err := manager.PrepareSubnet(context.Background(), "red", "instance-subnet"); err != nil {
		t.Fatal(err)
	}
	if len(routes.routes) != 1 || routes.routes[0].PublicAddress == nil || routes.routes[0].PublicAddress.AllocationID != "eipalloc-1" {
		t.Fatalf("route requests = %#v", routes.routes)
	}
}

// TestManagerRemovesUnusedNATAfterGrace verifies the route and VM are removed
// before an identity becomes reusable.
func TestManagerRemovesUnusedNATAfterGrace(t *testing.T) {
	db, err := localdb.Open(filepath.Join(t.TempDir(), "nat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	loader, err := config.NewLoader(config.LoaderOptions{
		Storage: storage.NewMock(), CacheStorage: storage.NewMock(), LocalDB: db, Logger: slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	loader.SetConfig(&config.Config{
		Cluster:   config.ClusterConfig{ID: "cluster"},
		Templates: map[string]config.TemplateConfig{"nat": {Kind: "nat"}},
		Groups:    map[string]map[string]config.GroupConfig{"red": {"nat": {Template: "nat", InstanceType: "small"}}},
		NAT: map[string]config.NATConfig{"red": {
			Group: "nat", PublicAddresses: []config.PublicAddress{{IPv4: "192.0.2.1", AllocationID: "eipalloc-1"}}, LastInstanceGracePeriod: config.Duration(time.Minute),
		}},
	})
	store := storage.NewMock()
	assignments, err := NewAssignmentStore(store)
	if err != nil {
		t.Fatal(err)
	}
	routes := &testRouteProvider{}
	creator := &testInstanceCreator{db: db}
	manager, err := NewManager(ManagerOptions{
		ConfigLoader: loader, LocalDB: db, Provider: routes, Assignments: assignments, Instances: creator,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.PrepareSubnet(ctx, "red", "instance-subnet"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("first preparation error = %v, want not ready", err)
	}
	if err := manager.PrepareSubnet(ctx, "red", "instance-subnet"); err != nil {
		t.Fatal(err)
	}
	current, err := assignments.Assignments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var assignment Assignment
	for _, assignment = range current {
	}
	emptySince := time.Now().UTC().Add(-2 * time.Minute)
	if err := assignments.Update(ctx, assignment.InstanceID, func(current *Assignment) { current.EmptySince = &emptySince }); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInstance(&localdb.Instance{
		ID: "instance", Tenant: "red", Group: "workers", SubnetID: "instance-subnet", Nonce: "instance", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(routes.removed) != 0 || len(creator.deleted) != 0 {
		t.Fatal("NAT was removed before its final dependent instance")
	}
	if err := db.DeleteInstance("instance"); err != nil {
		t.Fatal(err)
	}
	if err := assignments.Update(ctx, assignment.InstanceID, func(current *Assignment) { current.EmptySince = &emptySince }); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(routes.removed) != 1 || len(creator.deleted) != 1 {
		t.Fatalf("removed routes=%d deleted instances=%d, want 1 each", len(routes.removed), len(creator.deleted))
	}
	if remaining, err := assignments.Assignments(ctx); err != nil || len(remaining) != 0 {
		t.Fatalf("assignments after release=%d err=%v, want zero", len(remaining), err)
	}
}

// TestManagerRemovesNATImmediatelyWhenDisabled verifies switching to
// provider NAT bypasses the last-instance grace period.
func TestManagerRemovesNATImmediatelyWhenDisabled(t *testing.T) {
	db, err := localdb.Open(filepath.Join(t.TempDir(), "nat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	loader, err := config.NewLoader(config.LoaderOptions{
		Storage: storage.NewMock(), CacheStorage: storage.NewMock(), LocalDB: db, Logger: slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	configured := &config.Config{
		Cluster:   config.ClusterConfig{ID: "cluster"},
		Templates: map[string]config.TemplateConfig{"nat": {Kind: "nat"}},
		Groups:    map[string]map[string]config.GroupConfig{"red": {"nat": {Template: "nat", InstanceType: "small"}}},
		NAT: map[string]config.NATConfig{"red": {
			Group: "nat", PublicAddresses: []config.PublicAddress{{IPv4: "192.0.2.1", AllocationID: "eipalloc-1"}}, LastInstanceGracePeriod: config.Duration(time.Hour),
		}},
	}
	loader.SetConfig(configured)
	assignments, err := NewAssignmentStore(storage.NewMock())
	if err != nil {
		t.Fatal(err)
	}
	routes := &testRouteProvider{}
	creator := &testInstanceCreator{db: db}
	manager, err := NewManager(ManagerOptions{
		ConfigLoader: loader, LocalDB: db, Provider: routes, Assignments: assignments, Instances: creator,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.PrepareSubnet(ctx, "red", "instance-subnet"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("first preparation error = %v, want not ready", err)
	}
	if err := manager.PrepareSubnet(ctx, "red", "instance-subnet"); err != nil {
		t.Fatal(err)
	}
	loader.SetConfig(&config.Config{Cluster: configured.Cluster})
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(routes.removed) != 1 || len(creator.deleted) != 1 {
		t.Fatalf("removed routes=%d deleted instances=%d, want immediate cleanup", len(routes.removed), len(creator.deleted))
	}
}

// TestManagerReplacesNATAfterSustainedLoad verifies portable scaling signals
// trigger an A/B replacement while byte and packet rates alone do not.
func TestManagerReplacesNATAfterSustainedLoad(t *testing.T) {
	db, err := localdb.Open(filepath.Join(t.TempDir(), "nat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	loader, err := config.NewLoader(config.LoaderOptions{
		Storage: storage.NewMock(), CacheStorage: storage.NewMock(), LocalDB: db, Logger: slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	loader.SetConfig(&config.Config{
		Cluster:   config.ClusterConfig{ID: "cluster"},
		Templates: map[string]config.TemplateConfig{"nat": {Kind: "nat"}},
		Groups:    map[string]map[string]config.GroupConfig{"red": {"nat": {Template: "nat", InstanceType: "small"}}},
		NAT: map[string]config.NATConfig{"red": {
			Group: "nat", PublicAddresses: []config.PublicAddress{{IPv4: "192.0.2.1", AllocationID: "eipalloc-1"}}, LastInstanceGracePeriod: config.Duration(time.Minute),
			InstanceTypeLadder:  []string{"small", "large"},
			ScaleUpThresholds:   config.NATThresholds{CPUPercent: 80, ConntrackPercent: 80, PacketDropsPerSecond: 1},
			ScaleDownThresholds: config.NATThresholds{CPUPercent: 30, ConntrackPercent: 30},
			ScaleUpWindow:       config.Duration(2 * time.Minute), ScaleDownWindow: config.Duration(20 * time.Minute),
			Cooldown: config.Duration(10 * time.Minute), ReplacementTimeout: config.Duration(10 * time.Minute),
		}},
	})
	assignments, err := NewAssignmentStore(storage.NewMock())
	if err != nil {
		t.Fatal(err)
	}
	routes := &testRouteProvider{}
	creator := &testInstanceCreator{db: db}
	manager, err := NewManager(ManagerOptions{
		ConfigLoader: loader, LocalDB: db, Provider: routes, Assignments: assignments, Instances: creator,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.PrepareSubnet(ctx, "red", "instance-subnet"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("first preparation error = %v, want not ready", err)
	}
	if err := manager.PrepareSubnet(ctx, "red", "instance-subnet"); err != nil {
		t.Fatal(err)
	}
	current, err := assignments.Assignments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var original Assignment
	for _, original = range current {
	}
	observedAt := time.Now().UTC()
	lowCPU, lowConntrack, conntrackMax := 10.0, uint64(10), uint64(100)
	for i, counter := range []uint64{0, 1 << 40} {
		metrics := &proto.Metrics{
			CpuUsage: &lowCPU, ConntrackCount: &lowConntrack, ConntrackMax: &conntrackMax,
			NetworkInterface: &proto.InterfaceMetrics{RxBytes: counter, RxPackets: counter},
		}
		if err := manager.Observe(ctx, original.InstanceID, metrics, observedAt.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if !manager.scaleUpSince[original.InstanceID].IsZero() {
		t.Fatal("byte and packet rates started a scale-up window")
	}

	cpu, conntrackCount := 90.0, uint64(90)
	createErr := errors.New("create failed")
	creator.createErr = createErr
	for i, elapsed := range []time.Duration{2 * time.Second, 2*time.Minute + 3*time.Second} {
		counter := uint64(1<<40) + uint64(i+1)*1000
		metrics := &proto.Metrics{
			CpuUsage: &cpu, ConntrackCount: &conntrackCount, ConntrackMax: &conntrackMax,
			NetworkInterface: &proto.InterfaceMetrics{RxBytes: counter, RxPackets: counter},
		}
		err := manager.Observe(ctx, original.InstanceID, metrics, observedAt.Add(elapsed))
		if i == 0 && err != nil {
			t.Fatal(err)
		}
		if i == 1 && !errors.Is(err, createErr) {
			t.Fatalf("replacement error = %v, want %v", err, createErr)
		}
	}
	current, err = assignments.Assignments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(current) != 1 {
		t.Fatalf("assignments after failed replacement = %d, want 1", len(current))
	}
	creator.createErr = nil
	retryMetrics := &proto.Metrics{
		CpuUsage: &cpu, ConntrackCount: &conntrackCount, ConntrackMax: &conntrackMax,
		NetworkInterface: &proto.InterfaceMetrics{RxBytes: 1<<40 + 3000, RxPackets: 1<<40 + 3000},
	}
	if err := manager.Observe(ctx, original.InstanceID, retryMetrics, observedAt.Add(2*time.Minute+4*time.Second)); err != nil {
		t.Fatal(err)
	}
	current, err = assignments.Assignments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(current) != 2 || creator.request.InstanceType != "large" {
		t.Fatalf("assignments=%d replacement type=%q, want 2 and large", len(current), creator.request.InstanceType)
	}
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(routes.routes) != 2 || routes.routes[1].PublicAddress == nil || routes.routes[1].PublicAddress.AllocationID != "eipalloc-1" {
		t.Fatalf("routes after replacement = %#v", routes.routes)
	}
	current, err = assignments.Assignments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !current[original.InstanceID].Retiring {
		t.Fatalf("old assignment after cutover = %#v, want retiring", current[original.InstanceID])
	}
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(creator.deleted) != 1 || creator.deleted[0] != original.InstanceID {
		t.Fatalf("deleted instances = %v, want old instance", creator.deleted)
	}
}

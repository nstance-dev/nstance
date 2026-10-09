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

	"github.com/nstance-dev/nstance/v2/internal/proto"
	"github.com/nstance-dev/nstance/v2/internal/server/config"
	"github.com/nstance-dev/nstance/v2/internal/server/infra/mock"
	"github.com/nstance-dev/nstance/v2/internal/server/infra/provider"
	"github.com/nstance-dev/nstance/v2/internal/server/instances"
	"github.com/nstance-dev/nstance/v2/internal/server/localdb"
	"github.com/nstance-dev/nstance/v2/internal/server/reconciler"
	"github.com/nstance-dev/nstance/v2/internal/server/storage"
)

// testRouteProvider models owned routes and interrupted provider operations.
type testRouteProvider struct {
	routes          []provider.NATRouteRequest
	removed         []provider.NATRouteRequest
	targets         map[string]string
	publicInstance  string
	ensureErr       error
	failBeforeRoute bool
}

// EnsureNATRoute applies owned routing and address changes, optionally returning an error.
func (p *testRouteProvider) EnsureNATRoute(_ context.Context, req provider.NATRouteRequest) error {
	p.routes = append(p.routes, req)
	if req.PublicAddress != nil {
		p.publicInstance = req.ProviderInstanceID
	}
	if p.failBeforeRoute && p.ensureErr != nil {
		return p.ensureErr
	}
	if p.targets == nil {
		p.targets = make(map[string]string)
	}
	p.targets[req.InstanceSubnetID] = "target-" + req.ProviderInstanceID
	return p.ensureErr
}

// RemoveNATRoute removes the subnet's simulated translation route.
func (p *testRouteProvider) RemoveNATRoute(_ context.Context, req provider.NATRouteRequest) error {
	p.removed = append(p.removed, req)
	delete(p.targets, req.InstanceSubnetID)
	return nil
}

// testInstanceCreator inserts NAT instances with controllable readiness.
type testInstanceCreator struct {
	db        *localdb.DB
	request   instances.CreateInstanceRequest
	deleted   []string
	createErr error
	notReady  bool
}

// CreateNATInstance records and inserts one test NAT instance.
func (c *testInstanceCreator) CreateNATInstance(_ context.Context, req instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error) {
	c.request = req
	if c.createErr != nil {
		return nil, c.createErr
	}
	now := time.Now().UTC()
	providerID := "provider-" + req.InstanceID
	instance := &localdb.Instance{
		ID: req.InstanceID, Tenant: req.Tenant, Group: req.Group, SubnetID: "public", ProviderID: &providerID,
		Nonce: req.InstanceID, RegisteredAt: &now, HealthAt: &now, CreatedAt: now,
	}
	if c.notReady {
		instance.RegisteredAt = nil
		instance.HealthAt = nil
	}
	if err := c.db.CreateInstance(instance); err != nil {
		return nil, err
	}
	return &instances.CreateInstanceResponse{InstanceID: req.InstanceID, ProviderInstanceID: providerID}, nil
}

// CreateInstance rejects ordinary creation; NAT lifecycle owns these instances.
func (c *testInstanceCreator) CreateInstance(context.Context, instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error) {
	return nil, errors.New("ordinary instance creation is not available")
}

// DeleteInstance records deletion and removes the local test record.
func (c *testInstanceCreator) DeleteInstance(_ context.Context, _, instanceID string) error {
	c.deleted = append(c.deleted, instanceID)
	return c.db.DeleteInstance(instanceID)
}

// newTestManager supplies a NAT manager with one fixed address and controllable VMs.
func newTestManager(t *testing.T) (*Manager, *testRouteProvider, *testInstanceCreator) {
	t.Helper()
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
			"red": {Group: "nat", ReplacementTimeout: config.Duration(time.Minute), PublicAddresses: []config.PublicAddress{{IPv4: "192.0.2.1", AllocationID: "eipalloc-1"}}},
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
	return manager, routes, creator
}

// TestManagerCreatesHealthyNATBeforeInstallingRoute verifies nodes cannot pass
// preparation until the dedicated instance has registered and reported health.
func TestManagerCreatesHealthyNATBeforeInstallingRoute(t *testing.T) {
	manager, routes, creator := newTestManager(t)
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

// TestNATFailureRestoresOriginalSubnet verifies failure recovery preserves
// ownership, waits for readiness, and cuts over the route to exactly one replacement.
func TestNATFailureRestoresOriginalSubnet(t *testing.T) {
	for _, failure := range []string{"disconnect", "spot", "missing", "prepare", "retry"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			manager, routes, creator := newTestManager(t)
			db, assignments := manager.localDB, manager.assignments
			if err := manager.PrepareSubnet(ctx, "red", "worker-subnet"); !errors.Is(err, ErrNotReady) {
				t.Fatalf("initial preparation = %v, want not ready", err)
			}
			originalID := creator.request.InstanceID
			if err := manager.PrepareSubnet(ctx, "red", "worker-subnet"); err != nil {
				t.Fatal(err)
			}
			if err := db.CreateInstance(&localdb.Instance{
				ID: "worker", Tenant: "red", Group: "workers", SubnetID: "worker-subnet", Nonce: "worker", CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatal(err)
			}
			creator.notReady = true
			if failure == "disconnect" || failure == "spot" {
				rec, err := reconciler.New(reconciler.Options{
					InstanceManager: creator, NATReplacementHandler: manager.ReplaceInstance,
					ConfigLoader: manager.configLoader, LocalDB: db, Provider: mock.NewProvider(mock.Options{}),
					NotifyDrain: func(string, string, string, time.Time, time.Time) {}, IsLeader: func() bool { return true },
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := rec.Start(ctx); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(rec.Stop)
				eventType := reconciler.EventCheckInstance
				if failure == "spot" {
					eventType = reconciler.EventTerminationNotice
				}
				rec.Enqueue(reconciler.ReconcileEvent{Type: eventType, InstanceID: originalID, Cause: "disconnect"})
				deadline := time.Now().Add(2 * time.Second)
				for {
					current, err := db.ListInstances()
					if err != nil {
						t.Fatal(err)
					}
					if len(current) == 3 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("NAT disconnect did not create a replacement")
					}
					time.Sleep(time.Millisecond)
				}
				rec.Stop()
			} else {
				if failure == "retry" {
					if err := db.UpdateInstanceProviderState(originalID, []byte(`{"status":"deleted"}`)); err != nil {
						t.Fatal(err)
					}
				} else if err := db.DeleteInstance(originalID); err != nil {
					t.Fatal(err)
				}
				if failure == "retry" {
					creator.createErr = errors.New("provider unavailable")
					if err := manager.Reconcile(ctx); !errors.Is(err, creator.createErr) {
						t.Fatalf("replacement failure = %v, want provider error", err)
					}
					pending, err := assignments.Assignments(ctx)
					if err != nil || len(pending) != 1 || pending[originalID].ProviderID != "provider-"+originalID {
						t.Fatalf("assignments after failed creation = %#v, error = %v", pending, err)
					}
					creator.createErr = nil
				}
				if failure == "missing" || failure == "retry" {
					if err := manager.Reconcile(ctx); err != nil {
						t.Fatal(err)
					}
				} else if err := manager.PrepareSubnet(ctx, "red", "worker-subnet"); !errors.Is(err, ErrNotReady) {
					t.Fatalf("failed NAT preparation = %v, want not ready", err)
				}
			}
			replacementID := creator.request.InstanceID
			if replacementID == originalID {
				t.Fatal("failure reused the deleted NAT identity")
			}
			if err := manager.ReplaceInstance(ctx, originalID); err != nil {
				t.Fatal(err)
			}
			if err := manager.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			prepareErr := manager.PrepareSubnet(ctx, "red", "worker-subnet")
			if failure == "spot" {
				if prepareErr != nil {
					t.Fatalf("healthy predecessor stopped serving workers: %v", prepareErr)
				}
			} else if !errors.Is(prepareErr, ErrNotReady) {
				t.Fatalf("failed predecessor preparation = %v, want not ready", prepareErr)
			}
			current, err := assignments.Assignments(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(current) != 2 || current[replacementID].Replaces != originalID || current[replacementID].InstanceSubnetID != "worker-subnet" {
				t.Fatalf("replacement assignments = %#v", current)
			}
			if len(routes.routes) != 1 {
				t.Fatal("route switched before replacement readiness")
			}
			replacement, err := db.GetInstance(replacementID)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			replacement.RegisteredAt, replacement.HealthAt = &now, &now
			if err := db.UpdateInstance(replacement); err != nil {
				t.Fatal(err)
			}
			if err := manager.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			if len(routes.routes) != 2 {
				t.Fatalf("route count after readiness = %d, want 2", len(routes.routes))
			}
			route := routes.routes[1]
			if route.InstanceSubnetID != "worker-subnet" || route.ProviderInstanceID != "provider-"+replacementID || route.PreviousProviderInstanceID != "provider-"+originalID || route.PublicAddress == nil || route.PublicAddress.AllocationID != "eipalloc-1" {
				t.Fatalf("recovered route = %#v", route)
			}
			if err := manager.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			current, err = assignments.Assignments(ctx)
			if err != nil || len(current) != 1 || !current[replacementID].Routed || current[replacementID].Replaces != "" || routes.targets["worker-subnet"] != "target-provider-"+replacementID {
				t.Fatalf("assignments after recovery = %#v, error = %v", current, err)
			}
			if len(routes.removed) != 0 {
				t.Fatal("retiring NAT removed the replacement route")
			}
		})
	}
}

// TestNATCutoverSurvivesRestart verifies interrupted route/address changes recover
// after restart and VM loss, including rollback only to a healthy predecessor.
func TestNATCutoverSurvivesRestart(t *testing.T) {
	for _, scenario := range []string{"initial", "replacement", "replacement-before-route", "replacement-restore-failure", "replacement-both-deleted", "replacement-stale-predecessor", "replacement-unreported-predecessor", "replacement-not-attempted"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			manager, routes, creator := newTestManager(t)
			if err := manager.PrepareSubnet(ctx, "red", "worker-subnet"); !errors.Is(err, ErrNotReady) {
				t.Fatal(err)
			}
			originalID := creator.request.InstanceID
			if err := manager.localDB.CreateInstance(&localdb.Instance{
				ID: "worker", Tenant: "red", Group: "workers", SubnetID: "worker-subnet", Nonce: "worker", CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatal(err)
			}
			if scenario != "initial" {
				if err := manager.PrepareSubnet(ctx, "red", "worker-subnet"); err != nil {
					t.Fatal(err)
				}
				if err := manager.ReplaceInstance(ctx, originalID); err != nil {
					t.Fatal(err)
				}
			}
			interruptedID := creator.request.InstanceID
			if scenario != "replacement-not-attempted" {
				routes.ensureErr = errors.New("interrupted provider response")
				routes.failBeforeRoute = scenario == "replacement-before-route"
				var err error
				if scenario == "initial" {
					err = manager.PrepareSubnet(ctx, "red", "worker-subnet")
				} else {
					err = manager.Reconcile(ctx)
				}
				if !errors.Is(err, routes.ensureErr) {
					t.Fatalf("interrupted operation = %v", err)
				}
			}
			if scenario == "replacement-stale-predecessor" || scenario == "replacement-unreported-predecessor" {
				cfg := manager.configLoader.GetCurrent()
				cfg.Shard.HealthCheckInterval = config.Duration(time.Minute)
				manager.configLoader.SetConfig(cfg)
				previous, err := manager.localDB.GetInstance(originalID)
				if err != nil {
					t.Fatal(err)
				}
				stale := time.Now().UTC().Add(-4 * time.Minute)
				previous.HealthAt = &stale
				if scenario == "replacement-unreported-predecessor" {
					previous.HealthAt = nil
				}
				if err := manager.localDB.UpdateInstance(previous); err != nil {
					t.Fatal(err)
				}
			}
			routes.ensureErr = nil
			if err := manager.localDB.DeleteInstance(interruptedID); err != nil {
				t.Fatal(err)
			}
			if scenario == "replacement-both-deleted" {
				if err := manager.localDB.DeleteInstance(originalID); err != nil {
					t.Fatal(err)
				}
			}
			// Reload the durable store and manager, not their previous in-memory state.
			assignments, err := NewAssignmentStore(manager.assignments.storage)
			if err != nil {
				t.Fatal(err)
			}
			manager, err = NewManager(ManagerOptions{
				ConfigLoader: manager.configLoader, LocalDB: manager.localDB,
				Provider: routes, Assignments: assignments, Instances: creator,
			})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "initial" {
				if err := manager.PrepareSubnet(ctx, "red", "worker-subnet"); !errors.Is(err, ErrNotReady) {
					t.Fatal(err)
				}
			} else {
				if err := assignments.Update(ctx, interruptedID, func(current *Assignment) {
					current.CreatedAt = time.Now().UTC().Add(-2 * time.Minute)
				}); err != nil {
					t.Fatal(err)
				}
				if scenario == "replacement-restore-failure" {
					routes.ensureErr = errors.New("restore failed")
					if err := manager.Reconcile(ctx); !errors.Is(err, routes.ensureErr) {
						t.Fatalf("failed restoration = %v", err)
					}
					retained, err := assignments.Assignments(ctx)
					if err != nil || retained[interruptedID].ProviderID == "" || retained[interruptedID].Deleting {
						t.Fatalf("failed restoration lost recovery state: assignments=%#v error=%v", retained, err)
					}
					routes.ensureErr = nil
				}
				if err := manager.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
				if scenario == "replacement-stale-predecessor" || scenario == "replacement-unreported-predecessor" {
					if routes.targets["worker-subnet"] != "target-provider-"+interruptedID {
						t.Fatalf("route changed without a healthy NAT: %q", routes.targets["worker-subnet"])
					}
					manager, err = NewManager(ManagerOptions{
						ConfigLoader: manager.configLoader, LocalDB: manager.localDB,
						Provider: routes, Assignments: assignments, Instances: creator,
					})
					if err != nil {
						t.Fatal(err)
					}
					if err := manager.Reconcile(ctx); err != nil {
						t.Fatal(err)
					}
					if routes.targets["worker-subnet"] != "target-provider-"+interruptedID {
						t.Fatal("route changed before fresh health")
					}
					previous, err := manager.localDB.GetInstance(originalID)
					if err != nil {
						t.Fatal(err)
					}
					fresh := time.Now().UTC()
					previous.HealthAt = &fresh
					if err := manager.localDB.UpdateInstance(previous); err != nil {
						t.Fatal(err)
					}
					routes.ensureErr = errors.New("restore after health failed")
					routes.failBeforeRoute = true
					if err := manager.Reconcile(ctx); !errors.Is(err, routes.ensureErr) {
						t.Fatalf("restoration error = %v", err)
					}
					routes.ensureErr = nil
					if err := manager.Reconcile(ctx); err != nil {
						t.Fatal(err)
					}
				}
				if scenario != "replacement-both-deleted" {
					retained, err := assignments.Assignments(ctx)
					if err != nil || len(retained) != 1 || retained[originalID].InstanceID != originalID {
						t.Fatalf("aborted replacement not released: assignments=%#v error=%v", retained, err)
					}
					if _, err := manager.localDB.GetInstance(originalID); err != nil {
						t.Fatalf("healthy predecessor was removed: %v", err)
					}
					if routes.targets["worker-subnet"] != "target-provider-"+originalID || routes.publicInstance != "provider-"+originalID {
						t.Fatalf("predecessor not restored: target=%q public instance=%q", routes.targets["worker-subnet"], routes.publicInstance)
					}
					return
				}
				if err := manager.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := manager.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			if routes.targets["worker-subnet"] != "target-provider-"+creator.request.InstanceID || creator.request.InstanceID == interruptedID {
				t.Fatalf("recovered target=%q replacement=%q", routes.targets["worker-subnet"], creator.request.InstanceID)
			}
		})
	}
}

// TestRouteRequestSelectsTranslationPrefix verifies NAT64 does not replace the
// native IPv6 default route.
func TestRouteRequestSelectsTranslationPrefix(t *testing.T) {
	manager := &Manager{}
	cfg := &config.Config{
		Cluster: config.ClusterConfig{ID: "cluster"},
		NAT:     map[string]config.NATConfig{"red": {Mode: "nat64"}},
	}
	request := manager.routeRequest(cfg, nil, Assignment{
		Tenant: "red", InstanceSubnetID: "subnet", ProviderID: "instance",
	})
	if request.DestinationCIDR != "64:ff9b::/96" {
		t.Fatalf("destination = %q, want NAT64 prefix", request.DestinationCIDR)
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

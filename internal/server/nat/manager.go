// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package nat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/puidv7/puidv7-go"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/infra/provider"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/localdb"
)

// ErrNotReady indicates that a required NAT instance has not registered and reported health.
var ErrNotReady = errors.New("managed NAT is not ready")

// InstanceCreator creates dedicated NAT instances.
type InstanceCreator interface {
	CreateNATInstance(context.Context, instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error)
	DeleteInstance(context.Context, string, string) error
}

// routeProvider manages provider routes used by the NAT manager.
type routeProvider interface {
	EnsureNATRoute(context.Context, provider.NATRouteRequest) error
	RemoveNATRoute(context.Context, provider.NATRouteRequest) error
}

// ManagerOptions contains dependencies for managed NAT preparation.
type ManagerOptions struct {
	ConfigLoader *config.Loader
	LocalDB      *localdb.DB
	Provider     routeProvider
	Assignments  *AssignmentStore
	Instances    InstanceCreator
	Logger       *slog.Logger
}

// Manager prepares dedicated NAT instances and routes before instance creation.
type Manager struct {
	configLoader   *config.Loader
	localDB        *localdb.DB
	provider       routeProvider
	assignments    *AssignmentStore
	instances      InstanceCreator
	logger         *slog.Logger
	mu             sync.Mutex
	metrics        map[string]metricSample
	scaleUpSince   map[string]time.Time
	scaleDownSince map[string]time.Time
}

// metricSample is one server-side scaling observation for a NAT instance.
type metricSample struct {
	at        time.Time
	drops     uint64
	cpu       float64
	conntrack float64
}

// NewManager creates a managed NAT manager.
func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.ConfigLoader == nil || opts.LocalDB == nil || opts.Provider == nil || opts.Assignments == nil || opts.Instances == nil {
		return nil, fmt.Errorf("config loader, local database, provider, assignments, and instance creator are required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Manager{
		configLoader:   opts.ConfigLoader,
		localDB:        opts.LocalDB,
		provider:       opts.Provider,
		assignments:    opts.Assignments,
		instances:      opts.Instances,
		logger:         opts.Logger,
		metrics:        make(map[string]metricSample),
		scaleUpSince:   make(map[string]time.Time),
		scaleDownSince: make(map[string]time.Time),
	}, nil
}

// Observe evaluates one dedicated NAT health report for vertical scaling.
func (m *Manager) Observe(ctx context.Context, instanceID string, metrics *proto.Metrics, observedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if metrics == nil || metrics.NetworkInterface == nil || metrics.NetworkInterfaceError != nil || metrics.CpuUsage == nil || metrics.ConntrackCount == nil || metrics.ConntrackMax == nil || *metrics.ConntrackMax == 0 {
		delete(m.scaleUpSince, instanceID)
		delete(m.scaleDownSince, instanceID)
		return nil
	}
	current := metricSample{
		at:        observedAt,
		drops:     metrics.NetworkInterface.RxDrops + metrics.NetworkInterface.TxDrops,
		cpu:       *metrics.CpuUsage,
		conntrack: 100 * float64(*metrics.ConntrackCount) / float64(*metrics.ConntrackMax),
	}
	previous, ok := m.metrics[instanceID]
	m.metrics[instanceID] = current
	if !ok || !current.at.After(previous.at) || current.drops < previous.drops {
		return nil
	}
	assignments, err := m.assignments.Assignments(ctx)
	if err != nil {
		return err
	}
	var assignment Assignment
	for _, candidate := range assignments {
		if candidate.InstanceID == instanceID && candidate.Replaces == "" && !candidate.Deleting {
			assignment = candidate
			break
		}
	}
	if assignment.InstanceID == "" {
		return nil
	}
	cfg := m.configLoader.GetCurrent()
	natConfig, ok := cfg.NAT[assignment.Tenant]
	if !ok || natConfig.Group == "" {
		return nil
	}
	index := -1
	for i, instanceType := range natConfig.InstanceTypeLadder {
		if instanceType == assignment.InstanceType {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("NAT instance type %q is not in the configured ladder", assignment.InstanceType)
	}
	if assignment.LastScaleAt != nil && observedAt.Sub(*assignment.LastScaleAt) < natConfig.Cooldown.Duration() {
		return nil
	}
	seconds := current.at.Sub(previous.at).Seconds()
	drops := float64(current.drops-previous.drops) / seconds
	scaleUp := current.cpu > natConfig.ScaleUpThresholds.CPUPercent || current.conntrack > natConfig.ScaleUpThresholds.ConntrackPercent || drops > natConfig.ScaleUpThresholds.PacketDropsPerSecond
	scaleDown := current.cpu <= natConfig.ScaleDownThresholds.CPUPercent && current.conntrack <= natConfig.ScaleDownThresholds.ConntrackPercent && drops <= natConfig.ScaleDownThresholds.PacketDropsPerSecond
	if scaleUp && index+1 < len(natConfig.InstanceTypeLadder) {
		delete(m.scaleDownSince, instanceID)
		if m.scaleUpSince[instanceID].IsZero() {
			m.scaleUpSince[instanceID] = observedAt
			return nil
		}
		if observedAt.Sub(m.scaleUpSince[instanceID]) >= natConfig.ScaleUpWindow.Duration() {
			return m.startReplacement(ctx, cfg, natConfig, assignment, natConfig.InstanceTypeLadder[index+1], observedAt)
		}
		return nil
	}
	delete(m.scaleUpSince, instanceID)
	if scaleDown && index > 0 {
		if m.scaleDownSince[instanceID].IsZero() {
			m.scaleDownSince[instanceID] = observedAt
			return nil
		}
		if observedAt.Sub(m.scaleDownSince[instanceID]) >= natConfig.ScaleDownWindow.Duration() {
			return m.startReplacement(ctx, cfg, natConfig, assignment, natConfig.InstanceTypeLadder[index-1], observedAt)
		}
		return nil
	}
	delete(m.scaleDownSince, instanceID)
	return nil
}

// startReplacement creates a second NAT instance before moving traffic to it.
func (m *Manager) startReplacement(ctx context.Context, cfg *config.Config, natConfig config.NATConfig, current Assignment, instanceType string, now time.Time) error {
	instanceID, err := puidv7.New(cfg.Templates[cfg.Groups[current.Tenant][natConfig.Group].Template].Kind)
	if err != nil {
		return fmt.Errorf("generate replacement NAT instance ID: %w", err)
	}
	_, err = m.assignments.ClaimReplacement(ctx, current, instanceID, instanceType, now)
	if err != nil {
		return err
	}
	if _, err := m.instances.CreateNATInstance(ctx, instances.CreateInstanceRequest{
		InstanceID: instanceID, Tenant: current.Tenant, Group: natConfig.Group,
		InstanceType: instanceType,
	}); err != nil {
		createErr := fmt.Errorf("create replacement NAT instance: %w", err)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if releaseErr := m.assignments.Release(cleanupCtx, instanceID); releaseErr != nil {
			return errors.Join(createErr, fmt.Errorf("release replacement NAT assignment: %w", releaseErr))
		}
		return createErr
	}
	delete(m.scaleUpSince, current.InstanceID)
	delete(m.scaleDownSince, current.InstanceID)
	m.logger.Info("Started NAT instance replacement", "tenant", current.Tenant, "subnet_id", current.InstanceSubnetID, "instance_type", instanceType)
	return nil
}

// PrepareSubnet ensures a healthy dedicated NAT instance and owned IPv4 route
// exist before a dependent instance is created.
func (m *Manager) PrepareSubnet(ctx context.Context, tenant, subnetID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg := m.configLoader.GetCurrent()
	natConfig, ok := cfg.NAT[tenant]
	if !ok {
		return nil
	}
	assignments, err := m.assignments.Assignments(ctx)
	if err != nil {
		return err
	}
	var assignment Assignment
	for _, candidate := range assignments {
		if candidate.Tenant == tenant && candidate.InstanceSubnetID == subnetID && candidate.Replaces == "" && !candidate.Deleting && !candidate.Retiring {
			assignment = candidate
			break
		}
	}
	if assignment.InstanceID == "" {
		group := cfg.Groups[tenant][natConfig.Group]
		template := cfg.Templates[group.Template]
		instanceID, err := puidv7.New(template.Kind)
		if err != nil {
			return fmt.Errorf("generate NAT instance ID: %w", err)
		}
		assignment, err = m.assignments.Claim(ctx, natConfig.PublicAddresses, tenant, subnetID, instanceID, group.InstanceType, time.Now().UTC())
		if err != nil {
			if errors.Is(err, ErrPublicAddressesExhausted) {
				return fmt.Errorf("%w: %w", instances.ErrSubnetDependencyCapacity, err)
			}
			return err
		}
	}
	instance, err := m.localDB.GetInstance(assignment.InstanceID)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := m.instances.CreateNATInstance(ctx, instances.CreateInstanceRequest{
			InstanceID: assignment.InstanceID,
			Tenant:     tenant,
			Group:      natConfig.Group,
		}); err != nil {
			return fmt.Errorf("create NAT instance: %w", err)
		}
		return ErrNotReady
	}
	if err != nil {
		return fmt.Errorf("read NAT instance: %w", err)
	}
	if instance.ProviderID == nil || instance.RegisteredAt == nil || instance.HealthAt == nil {
		return ErrNotReady
	}
	assignment.ProviderID = *instance.ProviderID
	if err := m.provider.EnsureNATRoute(ctx, m.routeRequest(cfg, assignments, assignment)); err != nil {
		return err
	}
	if assignment.InstanceType == "" {
		assignment.InstanceType = cfg.Groups[tenant][natConfig.Group].InstanceType
	}
	if err := m.assignments.Update(ctx, assignment.InstanceID, func(current *Assignment) {
		current.InstanceType = assignment.InstanceType
		current.ProviderID = assignment.ProviderID
		current.EmptySince = nil
		current.Deleting = false
		current.Routed = true
	}); err != nil {
		return fmt.Errorf("record NAT provider instance: %w", err)
	}
	m.logger.Info("Managed NAT route is ready", "tenant", tenant, "subnet_id", subnetID, "instance_id", instance.ID)
	return nil
}

// Run reconciles dedicated NAT instances until ctx is cancelled.
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := m.Reconcile(ctx); err != nil && ctx.Err() == nil {
			m.logger.Error("Failed to reconcile managed NAT", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Reconcile replaces and removes dedicated NAT instances as demand changes.
func (m *Manager) Reconcile(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := m.configLoader.GetCurrent()
	assignments, err := m.assignments.Assignments(ctx)
	if err != nil {
		return err
	}
	localInstances, err := m.localDB.ListInstances()
	if err != nil {
		return fmt.Errorf("list instances: %w", err)
	}
	natInstances := make(map[string]bool, len(assignments))
	for _, assignment := range assignments {
		natInstances[assignment.InstanceID] = true
	}
	now := time.Now().UTC()
	for _, assignment := range assignments {
		if assignment.Replaces == "" || assignment.Deleting {
			continue
		}
		natConfig, configured := cfg.NAT[assignment.Tenant]
		instance, instanceErr := m.localDB.GetInstance(assignment.InstanceID)
		ready := instanceErr == nil && instance.ProviderID != nil && instance.RegisteredAt != nil && instance.HealthAt != nil
		if !configured || natConfig.Group == "" || !ready {
			if !configured || natConfig.Group == "" || now.Sub(assignment.CreatedAt) >= natConfig.ReplacementTimeout.Duration() {
				return m.deleteAssignment(ctx, cfg, assignments, assignment)
			}
			continue
		}
		assignment.ProviderID = *instance.ProviderID
		if err := m.assignments.Update(ctx, assignment.InstanceID, func(current *Assignment) {
			current.ProviderID = assignment.ProviderID
		}); err != nil {
			return err
		}
		if err := m.provider.EnsureNATRoute(ctx, m.routeRequest(cfg, assignments, assignment)); err != nil {
			return fmt.Errorf("switch NAT route to replacement: %w", err)
		}
		if err := m.assignments.Promote(ctx, assignment.InstanceID, now); err != nil {
			return err
		}
		m.logger.Info("Promoted replacement NAT instance", "tenant", assignment.Tenant, "subnet_id", assignment.InstanceSubnetID, "instance_id", assignment.InstanceID)
		return nil
	}
	for _, assignment := range assignments {
		if assignment.Replaces != "" && !assignment.Deleting {
			continue
		}
		dependent := 0
		for _, instance := range localInstances {
			if instance.Tenant == assignment.Tenant && instance.SubnetID == assignment.InstanceSubnetID && !natInstances[instance.ID] {
				dependent++
			}
		}
		natConfig, configured := cfg.NAT[assignment.Tenant]
		dedicated := configured && natConfig.Group != ""
		if dependent != 0 && dedicated && !assignment.Deleting && !assignment.Retiring {
			if assignment.EmptySince != nil {
				if err := m.assignments.Update(ctx, assignment.InstanceID, func(current *Assignment) {
					current.EmptySince = nil
				}); err != nil {
					return err
				}
			}
			continue
		}
		if !assignment.Deleting && dedicated && !assignment.Retiring {
			if assignment.EmptySince == nil {
				if err := m.assignments.Update(ctx, assignment.InstanceID, func(current *Assignment) {
					current.EmptySince = &now
				}); err != nil {
					return err
				}
				continue
			}
			if now.Sub(*assignment.EmptySince) < natConfig.LastInstanceGracePeriod.Duration() {
				continue
			}
		}
		if err := m.deleteAssignment(ctx, cfg, assignments, assignment); err != nil {
			return err
		}
	}
	return nil
}

// deleteAssignment removes an unused NAT route and instance.
func (m *Manager) deleteAssignment(ctx context.Context, cfg *config.Config, assignments map[string]Assignment, assignment Assignment) error {
	if !assignment.Deleting {
		if assignment.Routed {
			if err := m.provider.RemoveNATRoute(ctx, m.routeRequest(cfg, assignments, assignment)); err != nil {
				return fmt.Errorf("remove NAT route: %w", err)
			}
		}
		if err := m.instances.DeleteInstance(ctx, assignment.Tenant, assignment.InstanceID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("delete NAT instance: %w", err)
		}
		if err := m.assignments.Update(ctx, assignment.InstanceID, func(current *Assignment) {
			current.Deleting = true
			current.Routed = false
		}); err != nil {
			return err
		}
	}
	if err := m.assignments.Release(ctx, assignment.InstanceID); err != nil {
		return err
	}
	m.logger.Info("Removed unused NAT instance", "tenant", assignment.Tenant, "subnet_id", assignment.InstanceSubnetID, "instance_id", assignment.InstanceID)
	return nil
}

// routeRequest builds provider routing input from one assignment snapshot.
func (m *Manager) routeRequest(cfg *config.Config, assignments map[string]Assignment, assignment Assignment) provider.NATRouteRequest {
	request := provider.NATRouteRequest{
		ClusterID: cfg.Cluster.ID, Tenant: assignment.Tenant, InstanceSubnetID: assignment.InstanceSubnetID,
		ProviderInstanceID: assignment.ProviderID,
		InstanceTag:        provider.NATNetworkTag(cfg.Cluster.ID, assignment.Tenant, assignment.InstanceSubnetID),
	}
	if assignment.PublicAddress != nil {
		request.PublicAddress = &provider.PublicAddress{
			IPv4: assignment.PublicAddress.IPv4, AllocationID: assignment.PublicAddress.AllocationID,
		}
	}
	if previous, ok := assignments[assignment.Replaces]; ok {
		request.PreviousProviderInstanceID = previous.ProviderID
	}
	return request
}

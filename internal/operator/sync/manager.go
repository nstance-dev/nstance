// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
	"github.com/nstance-dev/nstance/internal/proto"
)

// AnnotationManagedBy is the annotation key used to mark resources created by the sync manager.
// Resources with this annotation can be garbage collected when they become orphaned.
// User-created resources without this annotation are never deleted by the sync manager.
const AnnotationManagedBy = "nstance.dev/managed-by"

// AnnotationManagedByOperator is the value indicating the operator manages this resource.
const AnnotationManagedByOperator = "nstance-operator"

// syncRequest represents a sync operation to process.
type syncRequest struct {
	fullSync      bool
	reconcileOnly bool // just reconcile, don't fetch from shards
	shard         string
	groupEvent    *proto.GroupEvent
}

// Manager handles periodic config sync and group discovery.
type Manager struct {
	client        client.Client
	logger        logr.Logger
	recorder      record.EventRecorder
	interval      time.Duration
	namespace     string
	clusterName   string
	shardGroups   map[string]map[string]*proto.GroupStatus // shard -> groupKey -> group (includes etag)
	shardGroupsMu sync.RWMutex                             // protects shardGroups for controller updates
	connections   map[string]*grpc.ClientConn
	syncCh        chan syncRequest

	// Pause reconciliation while controller is pushing changes to shards
	pauseMu sync.Mutex
	paused  bool
}

// NewManager creates a new sync manager.
func NewManager(c client.Client, logger logr.Logger, recorder record.EventRecorder, interval time.Duration, namespace, clusterName string) *Manager {
	return &Manager{
		client:      c,
		logger:      logger,
		recorder:    recorder,
		interval:    interval,
		namespace:   namespace,
		clusterName: clusterName,
		shardGroups: make(map[string]map[string]*proto.GroupStatus),
		syncCh:      make(chan syncRequest, 100),
	}
}

// PauseReconciliation pauses MachinePool reconciliation.
// Use this while the controller is pushing changes to shards to avoid
// reconciling with incomplete aggregate data.
func (m *Manager) PauseReconciliation() {
	m.pauseMu.Lock()
	m.paused = true
	m.pauseMu.Unlock()
	m.logger.Info("reconciliation paused")
}

// ResumeReconciliation resumes MachinePool reconciliation and triggers
// an immediate reconcile with the current cached state.
func (m *Manager) ResumeReconciliation() {
	m.pauseMu.Lock()
	m.paused = false
	m.pauseMu.Unlock()
	m.logger.Info("reconciliation resumed")

	// Trigger reconcile with current cached state
	select {
	case m.syncCh <- syncRequest{reconcileOnly: true}:
	default:
	}
}

func (m *Manager) isPaused() bool {
	m.pauseMu.Lock()
	defer m.pauseMu.Unlock()
	return m.paused
}

// UpdateShardGroup updates the cached group status for a shard.
// Called by the controller after successful UpsertGroup to keep the cache consistent.
func (m *Manager) UpdateShardGroup(shard, groupKey string, status *proto.GroupStatus) {
	m.shardGroupsMu.Lock()
	defer m.shardGroupsMu.Unlock()
	if m.shardGroups[shard] == nil {
		m.shardGroups[shard] = make(map[string]*proto.GroupStatus)
	}
	m.shardGroups[shard][groupKey] = status
}

// Start begins syncing. It blocks until ctx is cancelled.
func (m *Manager) Start(ctx context.Context, connections map[string]*grpc.ClientConn) error {
	m.logger.Info("starting sync manager", "interval", m.interval, "shards", len(connections))
	m.connections = connections

	// Initial full sync - must succeed
	if err := m.fullSync(ctx); err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}

	// Start watch streams for each shard
	for shard, conn := range connections {
		go m.watchShard(ctx, shard, conn)
		go m.watchShardErrors(ctx, shard, conn)
	}

	// Process sync requests
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("sync manager stopping")
			return ctx.Err()

		case <-ticker.C:
			if err := m.fullSync(ctx); err != nil {
				m.logger.Error(err, "periodic sync failed")
			}

		case req := <-m.syncCh:
			switch {
			case req.reconcileOnly:
				// Just reconcile with current cached state
				if err := m.reconcileMachinePools(ctx); err != nil {
					m.logger.Error(err, "reconcile failed")
				}
			case req.fullSync:
				if err := m.fullSync(ctx); err != nil {
					m.logger.Error(err, "triggered sync failed")
				}
			default:
				// Apply event to cache, only reconcile if not paused
				m.applyGroupEvent(ctx, req.shard, req.groupEvent)
				if !m.isPaused() {
					if err := m.reconcileMachinePools(ctx); err != nil {
						m.logger.Error(err, "reconcile failed after event")
					}
				}
			}
		}
	}
}

// watchShard watches a single shard for group changes via WatchGroups.
func (m *Manager) watchShard(ctx context.Context, shard string, conn *grpc.ClientConn) {
	log := m.logger.WithValues("shard", shard)
	c := proto.NewOperatorServiceClient(conn)

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stream, err := c.WatchGroups(ctx, &emptypb.Empty{})
		if err != nil {
			log.Error(err, "failed to start watch, retrying", "backoff", backoff)
			m.markShardUnreachable(ctx, shard)
			time.Sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		log.Info("watch stream connected")
		backoff = time.Second

		if err := m.consumeGroupStream(ctx, shard, stream); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Error(err, "watch stream error, reconnecting")
			m.markShardUnreachable(ctx, shard)

			// Request full sync on reconnect to catch missed events
			select {
			case m.syncCh <- syncRequest{fullSync: true}:
			default:
			}
		}
	}
}

// consumeGroupStream reads events from a WatchGroups stream.
func (m *Manager) consumeGroupStream(ctx context.Context, shard string, stream proto.OperatorService_WatchGroupsClient) error {
	for {
		event, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		select {
		case m.syncCh <- syncRequest{shard: shard, groupEvent: event}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// watchShardErrors watches a single shard for error events via WatchErrors.
func (m *Manager) watchShardErrors(ctx context.Context, shard string, conn *grpc.ClientConn) {
	log := m.logger.WithValues("shard", shard)
	c := proto.NewOperatorServiceClient(conn)

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stream, err := c.WatchErrors(ctx, &emptypb.Empty{})
		if err != nil {
			log.Error(err, "failed to start error watch, retrying", "backoff", backoff)
			time.Sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		log.V(1).Info("errors watch stream connected")
		backoff = time.Second

		if err := m.consumeErrorStream(ctx, shard, stream); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Error(err, "error watch stream error, reconnecting")
		}
	}
}

// consumeErrorStream reads events from a WatchErrors stream.
func (m *Manager) consumeErrorStream(ctx context.Context, shard string, stream proto.OperatorService_WatchErrorsClient) error {
	for {
		event, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		m.logger.Info("received error event", "shard", shard, "group", event.Group, "error", event.Error)

		if event.Group != "" {
			m.recordProviderError(ctx, shard, event.Group, event.Error)
		}
	}
}

// applyGroupEvent applies a single group event to the cached state.
func (m *Manager) applyGroupEvent(ctx context.Context, shard string, event *proto.GroupEvent) {
	m.shardGroupsMu.Lock()
	defer m.shardGroupsMu.Unlock()

	if m.shardGroups[shard] == nil {
		m.shardGroups[shard] = make(map[string]*proto.GroupStatus)
	}

	switch event.Type {
	case proto.GroupEvent_UPSERT:
		m.logger.Info("group upserted", "shard", shard, "group", event.Group.Key, "size", event.Group.Size)
		m.shardGroups[shard][event.Group.Key] = event.Group

	case proto.GroupEvent_DELETE:
		m.logger.Info("group deleted", "shard", shard, "group", event.Group.Key)
		delete(m.shardGroups[shard], event.Group.Key)
	}
}

// fullSync fetches all groups from all shards via ListGroups.
func (m *Manager) fullSync(ctx context.Context) error {
	m.logger.Info("starting full sync", "shards", len(m.connections))

	for shard, conn := range m.connections {
		if err := m.syncShard(ctx, shard, conn); err != nil {
			return fmt.Errorf("shard %s: %w", shard, err)
		}
	}

	return m.reconcileMachinePools(ctx)
}

// syncShard fetches and stores group data from a single shard.
func (m *Manager) syncShard(ctx context.Context, shard string, conn *grpc.ClientConn) error {
	c := proto.NewOperatorServiceClient(conn)

	// List all groups from this shard (each group includes its own etag)
	resp, err := c.ListGroups(ctx, &emptypb.Empty{})
	if err != nil {
		return err
	}

	m.shardGroupsMu.Lock()
	defer m.shardGroupsMu.Unlock()

	// Initialize shard map if needed
	if m.shardGroups[shard] == nil {
		m.shardGroups[shard] = make(map[string]*proto.GroupStatus)
	}

	// Always update cached state (provider_ids change without config changes).
	// Log only when config changes (etag differs).
	seen := make(map[string]bool)
	for _, g := range resp.Groups {
		seen[g.Key] = true
		cached := m.shardGroups[shard][g.Key]
		if cached == nil || cached.Etag != g.Etag {
			m.logger.Info("group config updated", "shard", shard, "group", g.Key, "size", g.Size)
		}
		m.shardGroups[shard][g.Key] = g
	}

	// Remove groups that no longer exist
	for key := range m.shardGroups[shard] {
		if !seen[key] {
			m.logger.Info("group removed", "shard", shard, "group", key)
			delete(m.shardGroups[shard], key)
		}
	}

	return nil
}

// reconcileMachinePools aggregates groups across shards and creates/updates MachinePools.
func (m *Manager) reconcileMachinePools(ctx context.Context) error {
	// Update NstanceShardGroup statuses first (per-shard providerIDs/replicas)
	m.updateShardGroupStatuses(ctx)

	aggregated := m.aggregateGroups()
	m.logger.Info("reconciling machine pools", "groups", len(aggregated))

	// Create or update pools for all aggregated groups (with aggregated sums)
	for groupKey, agg := range aggregated {
		if err := m.ensureMachinePool(ctx, groupKey, agg); err != nil {
			m.logger.Error(err, "failed to ensure MachinePool", "group", groupKey)
		}
	}

	return nil
}

// updateShardGroupStatuses updates the status of each NstanceShardGroup resource
// with the per-shard providerIDs and replicas from the cached group data.
func (m *Manager) updateShardGroupStatuses(ctx context.Context) {
	m.shardGroupsMu.RLock()
	// Copy the data we need so we can release the lock before doing K8s API calls
	type shardGroupUpdate struct {
		shard       string
		groupKey    string
		providerIDs []string
		replicas    int32
	}
	var updates []shardGroupUpdate
	for shard, groups := range m.shardGroups {
		for groupKey, group := range groups {
			ids := make([]string, len(group.ProviderIds))
			copy(ids, group.ProviderIds)
			sort.Strings(ids)
			updates = append(updates, shardGroupUpdate{
				shard:       shard,
				groupKey:    groupKey,
				providerIDs: ids,
				replicas:    group.ActualSize,
			})
		}
	}
	m.shardGroupsMu.RUnlock()

	for _, u := range updates {
		name := infrastructurev1beta1.NstanceShardGroupName(u.groupKey, u.shard)
		var sg infrastructurev1beta1.NstanceShardGroup
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: name}, &sg); err != nil {
			if client.IgnoreNotFound(err) == nil {
				continue
			}
			m.logger.Error(err, "failed to get NstanceShardGroup", "name", name)
			continue
		}

		if sg.Status.Replicas == u.replicas && slices.Equal(sg.Status.ProviderIDs, u.providerIDs) {
			continue
		}

		sg.Status.Replicas = u.replicas
		sg.Status.ProviderIDs = u.providerIDs
		now := metav1.Now()
		sg.Status.LastSyncTime = &now
		if err := m.client.Status().Update(ctx, &sg); err != nil {
			m.logger.Error(err, "failed to update NstanceShardGroup status", "name", name)
		}
	}
}

// aggregatedGroup holds aggregated group info across shards.
type aggregatedGroup struct {
	TotalReplicas int32
	Shards        []string
	Template      string
	InstanceType  string
	Vars          map[string]string
	SubnetPool    string
	IsStatic      bool     // true if any shard reports this group as static
	ProviderIDs   []string // Aggregated provider IDs across all shards
}

// aggregateGroups aggregates groups by key across all shards.
func (m *Manager) aggregateGroups() map[string]*aggregatedGroup {
	m.shardGroupsMu.RLock()
	defer m.shardGroupsMu.RUnlock()

	result := make(map[string]*aggregatedGroup)

	for shard, shardGroups := range m.shardGroups {
		for key, group := range shardGroups {
			if result[key] == nil {
				result[key] = &aggregatedGroup{
					Template:     group.Template,
					InstanceType: group.InstanceType,
					Vars:         group.Vars,
					SubnetPool:   group.SubnetPool,
				}
			}
			result[key].Shards = append(result[key].Shards, shard)
			result[key].TotalReplicas += group.Size
			result[key].ProviderIDs = append(result[key].ProviderIDs, group.ProviderIds...)
			if group.IsStatic {
				result[key].IsStatic = true
			}
		}
	}

	return result
}

// ensureMachinePool ensures a MachinePool and NstanceMachinePool exist for a group.
// It only sets replicas on initial creation; once a MachinePool exists, it becomes
// the source of truth for replica count.
func (m *Manager) ensureMachinePool(ctx context.Context, groupKey string, agg *aggregatedGroup) error {
	poolName := groupKey

	// Ensure NstanceMachinePool
	var nstancePool infrastructurev1beta1.NstanceMachinePool
	err := m.client.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: poolName}, &nstancePool)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return err
		}

		sort.Strings(agg.ProviderIDs)
		nstancePool = infrastructurev1beta1.NstanceMachinePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      poolName,
				Namespace: m.namespace,
				Annotations: map[string]string{
					AnnotationManagedBy: AnnotationManagedByOperator,
				},
			},
			Spec: infrastructurev1beta1.NstanceMachinePoolSpec{
				Group:          groupKey,
				Shards:         agg.Shards,
				Vars:           agg.Vars,
				ProviderIDList: agg.ProviderIDs,
			},
		}
		if agg.InstanceType != "" {
			nstancePool.Spec.InstanceType = ptr.To(agg.InstanceType)
		}
		if err := m.client.Create(ctx, &nstancePool); err != nil {
			return fmt.Errorf("create NstanceMachinePool: %w", err)
		}
		m.logger.Info("created NstanceMachinePool", "name", poolName, "shards", agg.Shards)
	} else {
		// Update NstanceMachinePool if spec has changed
		needsUpdate := false
		if nstancePool.Spec.Group != groupKey {
			nstancePool.Spec.Group = groupKey
			needsUpdate = true
		}
		newInstanceType := ptr.Deref(nstancePool.Spec.InstanceType, "")
		if newInstanceType != agg.InstanceType {
			if agg.InstanceType == "" {
				nstancePool.Spec.InstanceType = nil
			} else {
				nstancePool.Spec.InstanceType = ptr.To(agg.InstanceType)
			}
			needsUpdate = true
		}
		if !maps.Equal(nstancePool.Spec.Vars, agg.Vars) {
			nstancePool.Spec.Vars = agg.Vars
			needsUpdate = true
		}
		// Sort provider IDs for deterministic comparison
		sort.Strings(agg.ProviderIDs)
		if !slices.Equal(nstancePool.Spec.ProviderIDList, agg.ProviderIDs) {
			nstancePool.Spec.ProviderIDList = agg.ProviderIDs
			needsUpdate = true
		}
		if needsUpdate {
			if err := m.client.Update(ctx, &nstancePool); err != nil {
				return fmt.Errorf("update NstanceMachinePool: %w", err)
			}
			m.logger.Info("updated NstanceMachinePool", "name", poolName)
		}
	}

	// Update status with actual replica count
	replicas := int32(len(agg.ProviderIDs))
	if nstancePool.Status.Replicas != replicas {
		// Re-read to get the latest ResourceVersion
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: poolName}, &nstancePool); err != nil {
			return fmt.Errorf("re-read NstanceMachinePool for status update: %w", err)
		}
		nstancePool.Status.Replicas = replicas
		if err := m.client.Status().Update(ctx, &nstancePool); err != nil {
			return fmt.Errorf("update NstanceMachinePool status: %w", err)
		}
	}

	// Ensure MachinePool exists (CAPI controller owns its status/providerIDList,
	// reading from NstanceMachinePool via the InfraMachinePool contract)
	var machinePool clusterv1.MachinePool
	err = m.client.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: poolName}, &machinePool)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return err
		}

		machinePool = clusterv1.MachinePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      poolName,
				Namespace: m.namespace,
				Annotations: map[string]string{
					AnnotationManagedBy: AnnotationManagedByOperator,
				},
			},
			Spec: clusterv1.MachinePoolSpec{
				ClusterName: m.clusterName,
				Replicas:    &agg.TotalReplicas,
				Template: clusterv1.MachineTemplateSpec{
					Spec: clusterv1.MachineSpec{
						ClusterName: m.clusterName,
						Bootstrap: clusterv1.Bootstrap{
							DataSecretName: ptr.To(""),
						},
						InfrastructureRef: clusterv1.ContractVersionedObjectReference{
							Kind:     "NstanceMachinePool",
							Name:     poolName,
							APIGroup: "infrastructure.cluster.x-k8s.io",
						},
					},
				},
			},
		}
		if err := m.client.Create(ctx, &machinePool); err != nil {
			return fmt.Errorf("create MachinePool: %w", err)
		}
		m.logger.Info("created MachinePool", "name", poolName, "replicas", agg.TotalReplicas)
	}

	return nil
}

// markShardUnreachable marks all NstanceShardGroups for a shard as unreachable.
func (m *Manager) markShardUnreachable(ctx context.Context, shard string) {
	var shardGroups infrastructurev1beta1.NstanceShardGroupList
	if err := m.client.List(ctx, &shardGroups, client.InNamespace(m.namespace)); err != nil {
		m.logger.Error(err, "failed to list NstanceShardGroups")
		return
	}

	for i := range shardGroups.Items {
		sg := &shardGroups.Items[i]
		if sg.Spec.Shard != shard {
			continue
		}

		setShardGroupCondition(sg, infrastructurev1beta1.ConditionTypeShardReachable,
			metav1.ConditionFalse, "ShardUnreachable", fmt.Sprintf("Lost connection to shard %s", shard))

		if err := m.client.Status().Update(ctx, sg); err != nil {
			m.logger.Error(err, "failed to update NstanceShardGroup status", "name", sg.Name)
		}

		if m.recorder != nil {
			m.recorder.Event(sg, "Warning", "ShardUnreachable",
				fmt.Sprintf("Lost connection to shard %s", shard))
		}
	}
}

// recordProviderError records a provider error event on the NstanceShardGroup.
func (m *Manager) recordProviderError(ctx context.Context, shard, groupKey, errMsg string) {
	name := infrastructurev1beta1.NstanceShardGroupName(groupKey, shard)

	var shardGroup infrastructurev1beta1.NstanceShardGroup
	err := m.client.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: name}, &shardGroup)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			m.logger.V(1).Info("NstanceShardGroup not found for error event", "name", name)
		}
		return
	}

	if m.recorder != nil {
		m.recorder.Event(&shardGroup, "Warning", "ProviderError", errMsg)
	}
}

// setShardGroupCondition sets or updates a condition on the NstanceShardGroup.
func setShardGroupCondition(sg *infrastructurev1beta1.NstanceShardGroup, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	condition := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	}

	for i, c := range sg.Status.Conditions {
		if c.Type == condType {
			if c.Status != status {
				sg.Status.Conditions[i] = condition
			} else {
				sg.Status.Conditions[i].Reason = reason
				sg.Status.Conditions[i].Message = message
			}
			return
		}
	}

	sg.Status.Conditions = append(sg.Status.Conditions, condition)
}

// GetShardGroups returns all shard names the manager knows about.
func (m *Manager) GetShardGroups() []string {
	m.shardGroupsMu.RLock()
	defer m.shardGroupsMu.RUnlock()

	shards := make([]string, 0, len(m.shardGroups))
	for shard := range m.shardGroups {
		shards = append(shards, shard)
	}
	return shards
}

// GetShards returns all configured shard names.
func (m *Manager) GetShards() []string {
	if m.connections == nil {
		return nil
	}
	shards := make([]string, 0, len(m.connections))
	for shard := range m.connections {
		shards = append(shards, shard)
	}
	return shards
}

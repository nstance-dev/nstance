// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
	"github.com/nstance-dev/nstance/internal/operator/connection"
	"github.com/nstance-dev/nstance/internal/operator/sync"
	"github.com/nstance-dev/nstance/internal/proto"
)

const (
	NstanceMachinePoolFinalizer = "nstancemachinepool.infrastructure.cluster.x-k8s.io/finalizer"

	// ConditionTypeReady is the condition type for pool readiness.
	ConditionTypeReady = "Ready"

	// ConditionReasonInvalid indicates the pool configuration is invalid.
	ConditionReasonInvalid = "Invalid"

	// ConditionReasonDuplicateGroup indicates another pool already uses this group.
	ConditionReasonDuplicateGroup = "DuplicateGroup"

	// ConditionReasonReconciling indicates the pool is being reconciled.
	ConditionReasonReconciling = "Reconciling"

	// ConditionReasonReconciled indicates the pool was successfully reconciled.
	ConditionReasonReconciled = "Reconciled"
)

// NstanceMachinePoolReconciler reconciles a NstanceMachinePool object
type NstanceMachinePoolReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	ConnProvider *connection.Provider

	syncMgr     *sync.Manager
	clusterName string
}

// SetSyncManager sets the sync manager. Called by the leader manager after
// the sync manager is created.
func (r *NstanceMachinePoolReconciler) SetSyncManager(mgr *sync.Manager) {
	r.syncMgr = mgr
}

// SetClusterName sets the CAPI cluster name. Called by the leader manager after
// loading operator configuration.
func (r *NstanceMachinePoolReconciler) SetClusterName(name string) {
	r.clusterName = name
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstancemachinepools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstancemachinepools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstancemachinepools/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinepools,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinepools/status,verbs=get;update;patch

// Reconcile handles NstanceMachinePool reconciliation
func (r *NstanceMachinePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the NstanceMachinePool
	var nstancePool infrastructurev1beta1.NstanceMachinePool
	if err := r.Get(ctx, req.NamespacedName, &nstancePool); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !nstancePool.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&nstancePool, NstanceMachinePoolFinalizer) {
			log.Info("Cleaning up NstanceMachinePool", "name", nstancePool.Name, "group", nstancePool.Spec.Group)

			if err := r.deleteGroupFromShards(ctx, nstancePool.Spec.Group, nstancePool.Spec.Shards); err != nil {
				log.Error(err, "failed to delete group from shards", "group", nstancePool.Spec.Group)
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(&nstancePool, NstanceMachinePoolFinalizer)
			if err := r.Update(ctx, &nstancePool); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&nstancePool, NstanceMachinePoolFinalizer) {
		controllerutil.AddFinalizer(&nstancePool, NstanceMachinePoolFinalizer)
		if err := r.Update(ctx, &nstancePool); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Check for duplicate group: another NstanceMachinePool using the same group
	var allPools infrastructurev1beta1.NstanceMachinePoolList
	if err := r.List(ctx, &allPools, client.InNamespace(nstancePool.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	for _, other := range allPools.Items {
		if other.Name != nstancePool.Name && other.Spec.Group == nstancePool.Spec.Group {
			log.Info("Duplicate group detected", "name", nstancePool.Name, "group", nstancePool.Spec.Group, "existingPool", other.Name)
			nstancePool.Status.Ready = false
			nstancePool.Status.ObservedGeneration = nstancePool.Generation
			setConditionInPlace(&nstancePool, metav1.ConditionFalse, ConditionReasonDuplicateGroup,
				fmt.Sprintf("group %q is already used by NstanceMachinePool %q", nstancePool.Spec.Group, other.Name))
			if err := r.Status().Update(ctx, &nstancePool); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	// Find or create the MachinePool
	var machinePool clusterv1.MachinePool
	machinePoolKey := client.ObjectKey{
		Namespace: nstancePool.Namespace,
		Name:      nstancePool.Name,
	}
	if err := r.Get(ctx, machinePoolKey, &machinePool); err != nil {
		if errors.IsNotFound(err) {
			log.Info("MachinePool not found, creating", "pool", nstancePool.Name)
			machinePool = clusterv1.MachinePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nstancePool.Name,
					Namespace: nstancePool.Namespace,
				},
				Spec: clusterv1.MachinePoolSpec{
					ClusterName: r.clusterName,
					Template: clusterv1.MachineTemplateSpec{
						Spec: clusterv1.MachineSpec{
							ClusterName: r.clusterName,
							Bootstrap: clusterv1.Bootstrap{
								DataSecretName: ptr.To(""),
							},
							InfrastructureRef: clusterv1.ContractVersionedObjectReference{
								Kind:     "NstanceMachinePool",
								Name:     nstancePool.Name,
								APIGroup: "infrastructure.cluster.x-k8s.io",
							},
						},
					},
				},
			}
			if err := r.Create(ctx, &machinePool); err != nil {
				log.Error(err, "failed to create MachinePool", "pool", nstancePool.Name)
				return ctrl.Result{}, err
			}
			log.Info("Created MachinePool", "pool", nstancePool.Name)
		} else {
			return ctrl.Result{}, err
		}
	}

	// Check if we need to reconcile: skip if both generations haven't changed
	// AND the pool is already in a terminal ready state (not waiting for shard groups)
	if nstancePool.Status.ObservedGeneration == nstancePool.Generation &&
		nstancePool.Status.ObservedOwnerGeneration == machinePool.Generation &&
		nstancePool.Status.Ready {
		log.V(1).Info("Skipping reconciliation, nothing changed",
			"name", nstancePool.Name,
			"generation", nstancePool.Generation,
			"ownerGeneration", machinePool.Generation)
		return ctrl.Result{}, nil
	}

	// Calculate total replicas
	totalReplicas := int32(0)
	if machinePool.Spec.Replicas != nil {
		totalReplicas = *machinePool.Spec.Replicas
	}

	log.Info("Reconciling NstanceMachinePool",
		"name", nstancePool.Name,
		"group", nstancePool.Spec.Group,
		"replicas", totalReplicas)

	conns := r.ConnProvider.Get()
	if conns == nil {
		log.Info("Connections not ready yet, requeuing")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Validate that all specified shards have connections
	shards := nstancePool.Spec.Shards
	for _, shard := range shards {
		if _, ok := conns[shard]; !ok {
			log.Info("Shard not connected", "shard", shard)
			nstancePool.Status.Ready = false
			nstancePool.Status.ObservedGeneration = nstancePool.Generation
			setConditionInPlace(&nstancePool, metav1.ConditionFalse, ConditionReasonInvalid,
				fmt.Sprintf("shard %q is not available in operator config", shard))
			if err := r.Status().Update(ctx, &nstancePool); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	}

	if len(shards) == 0 {
		log.Error(nil, "no shards specified")
		nstancePool.Status.Ready = false
		nstancePool.Status.ObservedGeneration = nstancePool.Generation
		setConditionInPlace(&nstancePool, metav1.ConditionFalse, ConditionReasonInvalid, "spec.shards is required")
		if err := r.Status().Update(ctx, &nstancePool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Sort shards for deterministic distribution
	sort.Strings(shards)

	// Distribute replicas across shards
	shardSizes := distributeReplicas(totalReplicas, shards)

	// Pause sync reconciliation while we update all shards to avoid
	// reconciling with incomplete aggregate data
	if r.syncMgr != nil {
		r.syncMgr.PauseReconciliation()
		defer r.syncMgr.ResumeReconciliation()
	}

	// Create or update NstanceShardGroup for each shard
	// The NstanceShardGroup controller will handle calling UpsertGroup on the shard
	allReady := true
	anyStatic := false
	desiredShards := make(map[string]bool)

	for _, shard := range shards {
		desiredShards[shard] = true
		shardSize := shardSizes[shard]

		shardGroup, err := r.ensureShardGroup(ctx, &nstancePool, shard, shardSize)
		if err != nil {
			log.Error(err, "failed to ensure NstanceShardGroup", "shard", shard)
			nstancePool.Status.Ready = false
			nstancePool.Status.ObservedGeneration = nstancePool.Generation
			nstancePool.Status.ObservedOwnerGeneration = machinePool.Generation
			setConditionInPlace(&nstancePool, metav1.ConditionFalse, ConditionReasonInvalid, err.Error())
			if updateErr := r.Status().Update(ctx, &nstancePool); updateErr != nil {
				log.Error(updateErr, "failed to update status condition")
			}
			return ctrl.Result{}, nil
		}

		// Check if shard group is ready
		if !isShardGroupReady(shardGroup) {
			allReady = false
		}

		// Aggregate IsStatic: if any shard reports static, the pool is static
		if shardGroup.Status.IsStatic {
			anyStatic = true
		}
	}

	// Delete orphaned NstanceShardGroups (shards removed from spec.shards)
	if err := r.deleteOrphanedShardGroups(ctx, &nstancePool, desiredShards); err != nil {
		log.Error(err, "failed to delete orphaned shard groups")
		return ctrl.Result{}, err
	}

	// Mark pool as managed by sync once successfully reconciled (adopt user-created pools)
	if nstancePool.Annotations == nil || nstancePool.Annotations[sync.AnnotationManagedBy] != sync.AnnotationManagedByOperator {
		if nstancePool.Annotations == nil {
			nstancePool.Annotations = make(map[string]string)
		}
		nstancePool.Annotations[sync.AnnotationManagedBy] = sync.AnnotationManagedByOperator
		if err := r.Update(ctx, &nstancePool); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Update status with observed generations and IsStatic
	nstancePool.Status.Ready = allReady
	nstancePool.Status.IsStatic = anyStatic
	nstancePool.Status.ObservedGeneration = nstancePool.Generation
	nstancePool.Status.ObservedOwnerGeneration = machinePool.Generation
	if allReady {
		setConditionInPlace(&nstancePool, metav1.ConditionTrue, ConditionReasonReconciled, "Successfully reconciled")
	} else {
		setConditionInPlace(&nstancePool, metav1.ConditionFalse, ConditionReasonReconciling, "Waiting for shard groups to become ready")
	}
	if err := r.Status().Update(ctx, &nstancePool); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled NstanceMachinePool", "name", nstancePool.Name, "allReady", allReady)
	return ctrl.Result{}, nil
}

// deleteGroupFromShards calls DeleteGroup on the specified shards
func (r *NstanceMachinePoolReconciler) deleteGroupFromShards(ctx context.Context, groupKey string, shards []string) error {
	conns := r.ConnProvider.Get()
	if conns == nil {
		return fmt.Errorf("connections not ready")
	}

	var errs []error

	for _, shard := range shards {
		conn, ok := conns[shard]
		if !ok {
			errs = append(errs, fmt.Errorf("shard %s: connection not available", shard))
			continue
		}

		client := proto.NewOperatorServiceClient(conn)

		req := &proto.DeleteGroupRequest{
			Key: groupKey,
		}

		if _, err := client.DeleteGroup(ctx, req); err != nil {
			errs = append(errs, fmt.Errorf("shard %s: %w", shard, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to delete group from shards: %v", errs)
	}

	return nil
}

// deleteOrphanedShardGroups deletes NstanceShardGroups that are no longer in spec.shards
func (r *NstanceMachinePoolReconciler) deleteOrphanedShardGroups(
	ctx context.Context,
	pool *infrastructurev1beta1.NstanceMachinePool,
	desiredShards map[string]bool,
) error {
	log := log.FromContext(ctx)

	// List all NstanceShardGroups owned by this pool
	var shardGroups infrastructurev1beta1.NstanceShardGroupList
	if err := r.List(ctx, &shardGroups,
		client.InNamespace(pool.Namespace),
		client.MatchingLabels{"nstance.dev/group": pool.Spec.Group},
	); err != nil {
		return fmt.Errorf("failed to list shard groups: %w", err)
	}

	for _, sg := range shardGroups.Items {
		// Skip if this shard is still desired
		if desiredShards[sg.Spec.Shard] {
			continue
		}

		// Check if this NstanceShardGroup is owned by this pool
		isOwned := false
		for _, ref := range sg.OwnerReferences {
			if ref.UID == pool.UID {
				isOwned = true
				break
			}
		}
		if !isOwned {
			continue
		}

		// Delete the orphaned NstanceShardGroup
		log.Info("Deleting orphaned NstanceShardGroup", "name", sg.Name, "shard", sg.Spec.Shard)
		if err := r.Delete(ctx, &sg); err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete orphaned shard group %s: %w", sg.Name, err)
			}
		}
	}

	return nil
}

// getStringValue returns the value of a string pointer or empty string
func getStringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SetupWithManager sets up the controller with the Manager.
func (r *NstanceMachinePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1beta1.NstanceMachinePool{}).
		Owns(&infrastructurev1beta1.NstanceShardGroup{}).
		Watches(
			&clusterv1.MachinePool{},
			handler.EnqueueRequestsFromMapFunc(r.machinePoolToNstanceMachinePool),
		).
		Named("nstancemachinepool").
		Complete(r)
}

// machinePoolToNstanceMachinePool maps a MachinePool to the corresponding NstanceMachinePool.
// Since NstanceMachinePool and MachinePool share the same name/namespace, we can map directly.
func (r *NstanceMachinePoolReconciler) machinePoolToNstanceMachinePool(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	machinePool, ok := obj.(*clusterv1.MachinePool)
	if !ok {
		return nil
	}

	// Check if infrastructureRef points to a NstanceMachinePool
	if machinePool.Spec.Template.Spec.InfrastructureRef.Kind != "NstanceMachinePool" {
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Namespace: machinePool.Namespace,
				Name:      machinePool.Name,
			},
		},
	}
}

// setConditionInPlace sets the Ready condition on the pool without persisting.
func setConditionInPlace(pool *infrastructurev1beta1.NstanceMachinePool, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	condition := metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: pool.Generation,
	}

	for i, c := range pool.Status.Conditions {
		if c.Type == ConditionTypeReady {
			if c.Status != status {
				pool.Status.Conditions[i] = condition
			} else {
				pool.Status.Conditions[i].Reason = reason
				pool.Status.Conditions[i].Message = message
				pool.Status.Conditions[i].ObservedGeneration = pool.Generation
			}
			return
		}
	}

	pool.Status.Conditions = append(pool.Status.Conditions, condition)
}

// distributeReplicas distributes total replicas across shards.
// Remainder is distributed to first N shards (sorted order).
func distributeReplicas(total int32, shards []string) map[string]int32 {
	n := int32(len(shards))
	if n == 0 {
		return nil
	}

	base := total / n
	remainder := total % n

	result := make(map[string]int32, len(shards))
	for i, shard := range shards {
		size := base
		if int32(i) < remainder {
			size++
		}
		result[shard] = size
	}
	return result
}

// ensureShardGroup creates or updates the NstanceShardGroup for a shard.
func (r *NstanceMachinePoolReconciler) ensureShardGroup(
	ctx context.Context,
	pool *infrastructurev1beta1.NstanceMachinePool,
	shard string,
	size int32,
) (*infrastructurev1beta1.NstanceShardGroup, error) {
	name := infrastructurev1beta1.NstanceShardGroupName(pool.Spec.Group, shard)
	log := log.FromContext(ctx)

	var shardGroup infrastructurev1beta1.NstanceShardGroup
	err := r.Get(ctx, client.ObjectKey{Namespace: pool.Namespace, Name: name}, &shardGroup)
	if err != nil {
		if !errors.IsNotFound(err) {
			return nil, err
		}

		shardGroup = infrastructurev1beta1.NstanceShardGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: pool.Namespace,
				Labels: map[string]string{
					"nstance.dev/group": pool.Spec.Group,
					"nstance.dev/shard": shard,
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: pool.APIVersion,
						Kind:       pool.Kind,
						Name:       pool.Name,
						UID:        pool.UID,
						Controller: boolPtr(true),
					},
				},
			},
			Spec: infrastructurev1beta1.NstanceShardGroupSpec{
				Group:        pool.Spec.Group,
				Shard:        shard,
				Size:         size,
				Template:     pool.Spec.Template,
				InstanceType: getStringValue(pool.Spec.InstanceType),
				SubnetPool:   pool.Spec.SubnetPool,
				Vars:         pool.Spec.Vars,
			},
		}
		if err := r.Create(ctx, &shardGroup); err != nil {
			return nil, fmt.Errorf("create NstanceShardGroup: %w", err)
		}
		log.Info("Created NstanceShardGroup", "name", name, "size", size)
		return &shardGroup, nil
	}

	// Check if spec needs updating
	needsUpdate := false
	if shardGroup.Spec.Size != size {
		shardGroup.Spec.Size = size
		needsUpdate = true
	}
	if shardGroup.Spec.Template != pool.Spec.Template {
		shardGroup.Spec.Template = pool.Spec.Template
		needsUpdate = true
	}
	instanceType := getStringValue(pool.Spec.InstanceType)
	if shardGroup.Spec.InstanceType != instanceType {
		shardGroup.Spec.InstanceType = instanceType
		needsUpdate = true
	}
	if shardGroup.Spec.SubnetPool != pool.Spec.SubnetPool {
		shardGroup.Spec.SubnetPool = pool.Spec.SubnetPool
		needsUpdate = true
	}
	if !maps.Equal(shardGroup.Spec.Vars, pool.Spec.Vars) {
		shardGroup.Spec.Vars = pool.Spec.Vars
		needsUpdate = true
	}

	if needsUpdate {
		if err := r.Update(ctx, &shardGroup); err != nil {
			return nil, fmt.Errorf("update NstanceShardGroup: %w", err)
		}
		log.Info("Updated NstanceShardGroup", "name", name, "size", size)
	}

	return &shardGroup, nil
}

// isShardGroupReady checks if a NstanceShardGroup has Ready=True condition.
func isShardGroupReady(sg *infrastructurev1beta1.NstanceShardGroup) bool {
	for _, c := range sg.Status.Conditions {
		if c.Type == infrastructurev1beta1.ConditionTypeShardGroupReady {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func boolPtr(b bool) *bool {
	return &b
}

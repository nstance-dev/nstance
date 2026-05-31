// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
	"github.com/nstance-dev/nstance/internal/operator/connection"
	"github.com/nstance-dev/nstance/internal/proto"
)

const (
	// Event reasons
	EventReasonSyncSucceeded          = "SyncSucceeded"
	EventReasonShardUnreachable       = "ShardUnreachable"
	EventReasonProviderError          = "ProviderError"
	EventReasonConfigValidationFailed = "ConfigValidationFailed"
	EventReasonDeleteSucceeded        = "DeleteSucceeded"
	EventReasonDeleteFailed           = "DeleteFailed"

	// Finalizer for cleanup
	shardGroupFinalizer = "nstanceshardgroup.infrastructure.cluster.x-k8s.io/finalizer"
)

// NstanceShardGroupReconciler reconciles a NstanceShardGroup object
type NstanceShardGroupReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	ConnProvider *connection.Provider
	Recorder     record.EventRecorder
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstanceshardgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstanceshardgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstanceshardgroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile handles NstanceShardGroup reconciliation.
// This is a thin controller - when spec.size changes, it calls UpsertGroup on the shard
// and updates conditions based on the response.
func (r *NstanceShardGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var shardGroup infrastructurev1beta1.NstanceShardGroup
	if err := r.Get(ctx, req.NamespacedName, &shardGroup); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !shardGroup.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &shardGroup)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&shardGroup, shardGroupFinalizer) {
		controllerutil.AddFinalizer(&shardGroup, shardGroupFinalizer)
		if err := r.Update(ctx, &shardGroup); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Skip if we've already processed this generation
	if shardGroup.Status.ObservedGeneration == shardGroup.Generation {
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling NstanceShardGroup",
		"name", shardGroup.Name,
		"group", shardGroup.Spec.Group,
		"shard", shardGroup.Spec.Shard,
		"size", shardGroup.Spec.Size)

	conns := r.ConnProvider.Get()
	if conns == nil {
		log.Info("Connections not ready yet, requeuing")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	conn, ok := conns[shardGroup.Spec.Shard]
	if !ok {
		log.Error(nil, "shard not connected", "shard", shardGroup.Spec.Shard)
		r.setCondition(&shardGroup, infrastructurev1beta1.ConditionTypeShardReachable,
			metav1.ConditionFalse, EventReasonShardUnreachable,
			fmt.Sprintf("Shard %q is not connected", shardGroup.Spec.Shard))
		r.Recorder.Event(&shardGroup, "Warning", EventReasonShardUnreachable,
			fmt.Sprintf("Shard %q is not connected", shardGroup.Spec.Shard))
		if err := r.Status().Update(ctx, &shardGroup); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	r.setCondition(&shardGroup, infrastructurev1beta1.ConditionTypeShardReachable,
		metav1.ConditionTrue, "Connected", "Shard connection is healthy")

	status, err := r.upsertGroupOnShard(ctx, conn, &shardGroup)
	if err != nil {
		log.Error(err, "failed to upsert group on shard", "shard", shardGroup.Spec.Shard)
		r.setCondition(&shardGroup, infrastructurev1beta1.ConditionTypeShardGroupReady,
			metav1.ConditionFalse, EventReasonProviderError, err.Error())
		r.Recorder.Event(&shardGroup, "Warning", EventReasonProviderError, err.Error())
		if updateErr := r.Status().Update(ctx, &shardGroup); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	shardGroup.Status.Config = &infrastructurev1beta1.NstanceShardGroupConfig{
		Template:     status.Template,
		SubnetPool:   status.SubnetPool,
		InstanceType: status.InstanceType,
		Vars:         status.Vars,
	}
	shardGroup.Status.IsStatic = status.IsStatic
	now := metav1.Now()
	shardGroup.Status.LastSyncTime = &now
	shardGroup.Status.ObservedGeneration = shardGroup.Generation

	r.setCondition(&shardGroup, infrastructurev1beta1.ConditionTypeShardGroupReady,
		metav1.ConditionTrue, EventReasonSyncSucceeded, "Group synced successfully")
	r.setCondition(&shardGroup, infrastructurev1beta1.ConditionTypeConfigValid,
		metav1.ConditionTrue, "Valid", "Merged config is valid")

	if err := r.Status().Update(ctx, &shardGroup); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&shardGroup, "Normal", EventReasonSyncSucceeded, "Group synced successfully")
	log.Info("Successfully synced NstanceShardGroup", "name", shardGroup.Name)

	return ctrl.Result{}, nil
}

// reconcileDelete handles NstanceShardGroup deletion by calling DeleteGroup on the shard
func (r *NstanceShardGroupReconciler) reconcileDelete(
	ctx context.Context,
	shardGroup *infrastructurev1beta1.NstanceShardGroup,
) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(shardGroup, shardGroupFinalizer) {
		return ctrl.Result{}, nil
	}

	log.Info("Deleting NstanceShardGroup",
		"name", shardGroup.Name,
		"group", shardGroup.Spec.Group,
		"shard", shardGroup.Spec.Shard)

	conns := r.ConnProvider.Get()
	if conns == nil {
		log.Info("Connections not ready yet, requeuing deletion")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	conn, ok := conns[shardGroup.Spec.Shard]
	if !ok {
		log.Info("Shard not connected, removing finalizer anyway",
			"shard", shardGroup.Spec.Shard)
	} else {
		if err := r.deleteGroupOnShard(ctx, conn, shardGroup); err != nil {
			log.Error(err, "Failed to delete group on shard", "shard", shardGroup.Spec.Shard)
			r.Recorder.Event(shardGroup, "Warning", EventReasonDeleteFailed,
				fmt.Sprintf("Failed to delete group on shard %s: %v", shardGroup.Spec.Shard, err))
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		log.Info("Successfully deleted group on shard",
			"name", shardGroup.Name,
			"shard", shardGroup.Spec.Shard)
		r.Recorder.Event(shardGroup, "Normal", EventReasonDeleteSucceeded, "Group deleted from shard")
	}

	controllerutil.RemoveFinalizer(shardGroup, shardGroupFinalizer)
	if err := r.Update(ctx, shardGroup); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Finalizer removed, NstanceShardGroup will be deleted", "name", shardGroup.Name)
	return ctrl.Result{}, nil
}

// deleteGroupOnShard calls DeleteGroup on a single shard
func (r *NstanceShardGroupReconciler) deleteGroupOnShard(
	ctx context.Context,
	conn *grpc.ClientConn,
	sg *infrastructurev1beta1.NstanceShardGroup,
) error {
	client := proto.NewOperatorServiceClient(conn)
	req := &proto.DeleteGroupRequest{
		Key: sg.Spec.Group,
	}
	_, err := client.DeleteGroup(ctx, req)
	return err
}

// upsertGroupOnShard calls UpsertGroup on a single shard
func (r *NstanceShardGroupReconciler) upsertGroupOnShard(
	ctx context.Context,
	conn *grpc.ClientConn,
	sg *infrastructurev1beta1.NstanceShardGroup,
) (*proto.GroupStatus, error) {
	client := proto.NewOperatorServiceClient(conn)

	req := &proto.UpsertGroupRequest{
		Key: sg.Spec.Group,
		Config: &proto.GroupConfig{
			Template:     sg.Spec.Template,
			Size:         sg.Spec.Size,
			InstanceType: sg.Spec.InstanceType,
			SubnetPool:   sg.Spec.SubnetPool,
			Vars:         sg.Spec.Vars,
		},
	}

	return client.UpsertGroup(ctx, req)
}

// setCondition sets or updates a condition on the NstanceShardGroup
func (r *NstanceShardGroupReconciler) setCondition(
	sg *infrastructurev1beta1.NstanceShardGroup,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	now := metav1.Now()
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	}

	for i, c := range sg.Status.Conditions {
		if c.Type == conditionType {
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

// SetupWithManager sets up the controller with the Manager.
func (r *NstanceShardGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1beta1.NstanceShardGroup{}).
		Named("nstanceshardgroup").
		Complete(r)
}

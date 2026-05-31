// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
	"github.com/nstance-dev/nstance/internal/operator/connection"
	"github.com/nstance-dev/nstance/internal/operator/drain"
	"github.com/nstance-dev/nstance/internal/operator/node"
	"github.com/nstance-dev/nstance/internal/proto"
)

const (
	NstanceMachineFinalizer = "nstancemachine.infrastructure.cluster.x-k8s.io/finalizer"
)

// NstanceMachineReconciler reconciles a NstanceMachine object
type NstanceMachineReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	ConnProvider *connection.Provider
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstancemachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstancemachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstancemachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstancemachinetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes/status,verbs=get

// Reconcile handles NstanceMachine reconciliation
func (r *NstanceMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the NstanceMachine
	var nstanceMachine infrastructurev1beta1.NstanceMachine
	if err := r.Get(ctx, req.NamespacedName, &nstanceMachine); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !nstanceMachine.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&nstanceMachine, NstanceMachineFinalizer) {
			log.Info("Cleaning up NstanceMachine", "name", nstanceMachine.Name, "instanceID", nstanceMachine.Status.InstanceID)

			if nstanceMachine.Status.InstanceID != "" {
				// Skip DeleteInstance call if instance was already deleted server-side
				if hasCondition(nstanceMachine, drain.ConditionTypeServerDeleted) {
					log.Info("Instance already deleted server-side, skipping DeleteInstance call",
						"instanceID", nstanceMachine.Status.InstanceID)
				} else {
					// We guarantee Shard is present because it is set at creation time.
					// If for some reason it is missing (e.g. manual status edit), we cannot proceed safely without it.
					if nstanceMachine.Status.Shard == "" {
						log.Error(nil, "skipping DeleteInstance call: shard status is empty, server GC will clean up",
							"instanceID", nstanceMachine.Status.InstanceID)
					} else if err := r.deleteInstance(ctx, nstanceMachine.Status.Shard, nstanceMachine.Status.InstanceID); err != nil {
						log.Error(err, "failed to delete instance",
							"instanceID", nstanceMachine.Status.InstanceID,
							"shard", nstanceMachine.Status.Shard)
						return ctrl.Result{}, err
					}
				}
			}

			controllerutil.RemoveFinalizer(&nstanceMachine, NstanceMachineFinalizer)
			if err := r.Update(ctx, &nstanceMachine); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&nstanceMachine, NstanceMachineFinalizer) {
		controllerutil.AddFinalizer(&nstanceMachine, NstanceMachineFinalizer)
		if err := r.Update(ctx, &nstanceMachine); err != nil {
			return ctrl.Result{}, err
		}
	}

	log.Info("Reconciling NstanceMachine", "name", nstanceMachine.Name, "group", nstanceMachine.Spec.Group)

	// Create instance if not exists
	if nstanceMachine.Status.InstanceID == "" {
		instanceID, shard, err := r.createInstance(ctx, &nstanceMachine)
		if err != nil {
			log.Error(err, "failed to create instance")
			return ctrl.Result{}, err
		}

		nstanceMachine.Status.InstanceID = instanceID
		nstanceMachine.Status.Shard = shard
		if err := r.Status().Update(ctx, &nstanceMachine); err != nil {
			return ctrl.Result{}, err
		}

		log.Info("Created instance", "instanceID", instanceID, "shard", shard)

		// Ensure we return here to complete the status update before attempting to query status
		// This prevents race conditions where we might query status before the status update is persisted
		return ctrl.Result{}, nil
	}

	// Update instance status
	if err := r.updateInstanceStatus(ctx, &nstanceMachine); err != nil {
		log.Error(err, "failed to update instance status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// createInstance creates a new instance on the appropriate shard
func (r *NstanceMachineReconciler) createInstance(ctx context.Context, machine *infrastructurev1beta1.NstanceMachine) (string, string, error) {
	conns := r.ConnProvider.Get()
	if conns == nil {
		return "", "", fmt.Errorf("connections not ready")
	}

	// Get the owner Machine to check for FailureDomain (Zone)
	ownerMachine, err := capiutil.GetOwnerMachine(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		return "", "", fmt.Errorf("failed to get owner machine: %w", err)
	}

	var conn *grpc.ClientConn
	var shard string

	// If owner Machine specifies a FailureDomain, use that specific shard
	if ownerMachine != nil && ownerMachine.Spec.FailureDomain != "" {
		shard = ownerMachine.Spec.FailureDomain
		if c, ok := conns[shard]; ok {
			conn = c
			log.FromContext(ctx).Info("Using specific shard from FailureDomain", "shard", shard)
		} else {
			return "", "", fmt.Errorf("requested failure domain (shard) %q not available", shard)
		}
	} else {
		// Fallback to any available shard
		if len(conns) == 0 {
			return "", "", fmt.Errorf("no shard connections available")
		}
		for s, c := range conns {
			shard = s
			conn = c
			break
		}
	}

	client := proto.NewOperatorServiceClient(conn)

	req := &proto.CreateInstanceRequest{
		Config: &proto.InstanceConfig{
			Group:        machine.Spec.Group,
			InstanceType: getStringValue(machine.Spec.InstanceType),
			Vars:         machine.Spec.Vars,
		},
	}

	resp, err := client.CreateInstance(ctx, req)
	if err != nil {
		return "", "", fmt.Errorf("failed to create instance: %w", err)
	}

	return resp.InstanceId, shard, nil
}

// deleteInstance deletes an instance from a specific shard.
// Tolerates "not found" responses from the server (instance already deleted).
func (r *NstanceMachineReconciler) deleteInstance(ctx context.Context, shard, instanceID string) error {
	conns := r.ConnProvider.Get()
	if conns == nil {
		return fmt.Errorf("connections not ready")
	}

	conn, ok := conns[shard]
	if !ok {
		return fmt.Errorf("shard %q not connected", shard)
	}

	client := proto.NewOperatorServiceClient(conn)

	req := &proto.DeleteInstanceRequest{
		InstanceId: instanceID,
	}

	resp, err := client.DeleteInstance(ctx, req)
	if err != nil {
		return fmt.Errorf("shard %s: %w", shard, err)
	}

	log.FromContext(ctx).V(1).Info("Instance deletion response",
		"shard", shard,
		"instance_id", instanceID,
		"status", resp.Status)

	return nil
}

// updateInstanceStatus fetches instance status and updates the NstanceMachine status
func (r *NstanceMachineReconciler) updateInstanceStatus(ctx context.Context, machine *infrastructurev1beta1.NstanceMachine) error {
	if machine.Status.Shard == "" {
		// This should effectively never happen in a fresh system where Shard is set at creation
		return fmt.Errorf("instance status.shard is empty")
	}

	conns := r.ConnProvider.Get()
	if conns == nil {
		return fmt.Errorf("connections not ready")
	}

	shard := machine.Status.Shard
	conn, ok := conns[shard]
	if !ok {
		return fmt.Errorf("shard %q not connected", shard)
	}

	client := proto.NewOperatorServiceClient(conn)
	req := &proto.GetInstanceStatusRequest{
		InstanceId: machine.Status.InstanceID,
	}
	status, err := client.GetInstanceStatus(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to get instance status from shard %q: %w", shard, err)
	}

	updated := false

	if machine.Status.ProviderID != status.ProviderInstanceId {
		machine.Status.ProviderID = status.ProviderInstanceId
		updated = true
	}

	isReady := status.Status == "running"
	if machine.Status.Ready != isReady {
		machine.Status.Ready = isReady
		updated = true
	}

	if updated {
		if err := r.Status().Update(ctx, machine); err != nil {
			return err
		}
	}

	// Set Machine.Status.NodeRef if we have a providerID
	if machine.Status.ProviderID != "" {
		if err := r.setMachineNodeRef(ctx, machine); err != nil {
			log.FromContext(ctx).Error(err, "failed to set Machine.Status.NodeRef",
				"providerID", machine.Status.ProviderID)
		}
	}

	return nil
}

// setMachineNodeRef looks up the Node by providerID and sets Machine.Status.NodeRef on the owning Machine.
func (r *NstanceMachineReconciler) setMachineNodeRef(ctx context.Context, machine *infrastructurev1beta1.NstanceMachine) error {
	n, err := node.FindByProviderID(ctx, r.Client, machine.Status.ProviderID)
	if err != nil {
		return fmt.Errorf("failed to find node by providerID: %w", err)
	}
	if n == nil {
		return nil
	}

	ownerMachine, err := capiutil.GetOwnerMachine(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		return fmt.Errorf("failed to get owner machine: %w", err)
	}
	if ownerMachine == nil {
		return nil
	}

	if ownerMachine.Status.NodeRef.IsDefined() {
		return nil
	}

	ownerMachine.Status.NodeRef = clusterv1.MachineNodeReference{
		Name: n.Name,
	}

	if err := r.Status().Update(ctx, ownerMachine); err != nil {
		return fmt.Errorf("failed to update Machine.Status.NodeRef: %w", err)
	}

	log.FromContext(ctx).Info("Set Machine.Status.NodeRef",
		"machine", ownerMachine.Name,
		"node", n.Name)

	return nil
}

// hasCondition checks if the NstanceMachine has a condition of the given type with status True.
func hasCondition(machine infrastructurev1beta1.NstanceMachine, conditionType string) bool {
	for _, c := range machine.Status.Conditions {
		if c.Type == conditionType {
			return c.Status == "True"
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *NstanceMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1beta1.NstanceMachine{}).
		Named("nstancemachine").
		Complete(r)
}

// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
)

// NstanceClusterReconciler reconciles an NstanceCluster object.
// Its sole job is to mark the cluster infrastructure as provisioned,
// so that the Cluster API controllers can function as intended.
// We do not have any other functionality for NstanceCluster objects
// since Nstance manages infrastructure at the pool/machine level.
type NstanceClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstanceclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstanceclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=core,resources=serviceaccounts/token,verbs=create

// Reconcile ensures the NstanceCluster is marked as provisioned and ready.
func (r *NstanceClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var cluster infrastructurev1beta1.NstanceCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	updated := false

	if !ptr.Deref(cluster.Status.Initialization.Provisioned, false) {
		cluster.Status.Initialization.Provisioned = ptr.To(true)
		updated = true
	}

	hasReadyCondition := false
	for _, c := range cluster.Status.Conditions {
		if c.Type == "Ready" {
			hasReadyCondition = true
			break
		}
	}
	if !hasReadyCondition {
		cluster.Status.Conditions = append(cluster.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "Provisioned",
			Message:            "Nstance does not require cluster-level infrastructure",
			LastTransitionTime: metav1.Now(),
			ObservedGeneration: cluster.Generation,
		})
		updated = true
	}

	if updated {
		if err := r.Status().Update(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Marked NstanceCluster as provisioned", "name", cluster.Name)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NstanceClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1beta1.NstanceCluster{}).
		Named("nstancecluster").
		Complete(r)
}

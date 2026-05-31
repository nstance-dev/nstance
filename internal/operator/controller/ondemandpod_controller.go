// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
)

const (
	OnDemandGroupAnnotation        = "nstance.dev/on-demand-group"
	OnDemandInstanceTypeAnnotation = "nstance.dev/on-demand-instance-type"
	OnDemandPodFinalizer           = "ondemandpod.infrastructure.cluster.x-k8s.io/finalizer"
)

// OnDemandPodReconciler reconciles Pods with on-demand annotations
type OnDemandPodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstancemachines,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles on-demand pod reconciliation
func (r *OnDemandPodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the Pod
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Check if pod has on-demand annotation
	group, hasGroup := pod.Annotations[OnDemandGroupAnnotation]
	if !hasGroup {
		return ctrl.Result{}, nil
	}

	// Handle deletion
	if !pod.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&pod, OnDemandPodFinalizer) {
			log.Info("Cleaning up on-demand resources", "pod", pod.Name, "group", group)

			if err := r.cleanupMachine(ctx, &pod); err != nil {
				log.Error(err, "failed to cleanup machine")
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(&pod, OnDemandPodFinalizer)
			if err := r.Update(ctx, &pod); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&pod, OnDemandPodFinalizer) {
		controllerutil.AddFinalizer(&pod, OnDemandPodFinalizer)
		if err := r.Update(ctx, &pod); err != nil {
			return ctrl.Result{}, err
		}
	}

	log.Info("Reconciling on-demand pod", "pod", pod.Name, "group", group)

	machineName := fmt.Sprintf("on-demand-%s", pod.Name)

	// Ensure NstanceMachine exists
	var nstanceMachine infrastructurev1beta1.NstanceMachine
	err := r.Get(ctx, client.ObjectKey{
		Namespace: pod.Namespace,
		Name:      machineName,
	}, &nstanceMachine)

	if errors.IsNotFound(err) {
		instanceType := pod.Annotations[OnDemandInstanceTypeAnnotation]
		newNstanceMachine := &infrastructurev1beta1.NstanceMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      machineName,
				Namespace: pod.Namespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         "v1",
						Kind:               "Pod",
						Name:               pod.Name,
						UID:                pod.UID,
						Controller:         &[]bool{true}[0],
						BlockOwnerDeletion: &[]bool{true}[0],
					},
				},
			},
			Spec: infrastructurev1beta1.NstanceMachineSpec{
				Group: group,
			},
		}

		if instanceType != "" {
			newNstanceMachine.Spec.InstanceType = &instanceType
		}

		if err := r.Create(ctx, newNstanceMachine); err != nil {
			if !errors.IsAlreadyExists(err) {
				log.Error(err, "failed to create NstanceMachine")
				return ctrl.Result{}, err
			}
			log.Info("NstanceMachine already exists", "name", machineName)
		} else {
			log.Info("Created NstanceMachine", "name", machineName, "group", group)
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Ensure CAPI Machine exists
	var machine clusterv1.Machine
	err = r.Get(ctx, client.ObjectKey{
		Namespace: pod.Namespace,
		Name:      machineName,
	}, &machine)

	if errors.IsNotFound(err) {
		capiMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      machineName,
				Namespace: pod.Namespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         "v1",
						Kind:               "Pod",
						Name:               pod.Name,
						UID:                pod.UID,
						Controller:         &[]bool{true}[0],
						BlockOwnerDeletion: &[]bool{true}[0],
					},
				},
			},
			Spec: clusterv1.MachineSpec{
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					Kind: "NstanceMachine",
					Name: machineName,
				},
			},
		}

		if err := r.Create(ctx, capiMachine); err != nil {
			if !errors.IsAlreadyExists(err) {
				log.Error(err, "failed to create CAPI Machine")
				return ctrl.Result{}, err
			}
			log.Info("CAPI Machine already exists", "name", machineName)
		} else {
			log.Info("Created CAPI Machine for on-demand pod", "pod", pod.Name, "machine", machineName)
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// cleanupMachine deletes the associated machine if it exists
func (r *OnDemandPodReconciler) cleanupMachine(ctx context.Context, pod *corev1.Pod) error {
	machineName := fmt.Sprintf("on-demand-%s", pod.Name)

	// Delete CAPI Machine (will cascade to NstanceMachine via ownerReferences)
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: pod.Namespace,
		},
	}

	err := r.Delete(ctx, machine)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager
func (r *OnDemandPodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
			pod, ok := object.(*corev1.Pod)
			if !ok {
				return false
			}
			_, hasAnnotation := pod.Annotations[OnDemandGroupAnnotation]
			return hasAnnotation
		})).
		Named("ondemandpod").
		Complete(r)
}

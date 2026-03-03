// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package webhooks

import (
	"context"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
)

// NstanceMachinePoolValidator validates NstanceMachinePool resources.
// It prevents modification of template and subnet pool when the group is backed by static server config.
type NstanceMachinePoolValidator struct{}

var _ webhook.CustomValidator = &NstanceMachinePoolValidator{}

// ValidateCreate implements webhook.CustomValidator.
func (v *NstanceMachinePoolValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator.
// Rejects changes to spec.template or spec.subnetPool when status.IsStatic is true.
func (v *NstanceMachinePoolValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldPool, ok := oldObj.(*infrastructurev1beta1.NstanceMachinePool)
	if !ok {
		return nil, fmt.Errorf("expected NstanceMachinePool, got %T", oldObj)
	}
	newPool, ok := newObj.(*infrastructurev1beta1.NstanceMachinePool)
	if !ok {
		return nil, fmt.Errorf("expected NstanceMachinePool, got %T", newObj)
	}

	if !oldPool.Status.IsStatic {
		return nil, nil
	}

	if oldPool.Spec.Template != newPool.Spec.Template {
		return nil, fmt.Errorf("cannot modify spec.template for static group %q: template is defined in server config", oldPool.Spec.Group)
	}

	if oldPool.Spec.SubnetPool != newPool.Spec.SubnetPool {
		return nil, fmt.Errorf("cannot modify spec.subnetPool for static group %q: subnet pool is defined in server config", oldPool.Spec.Group)
	}

	if !slices.Equal(oldPool.Spec.Shards, newPool.Spec.Shards) {
		return nil, fmt.Errorf("cannot modify spec.shards for static group %q: shards are determined by server config", oldPool.Spec.Group)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator.
func (v *NstanceMachinePoolValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// SetupWithManager registers the webhook with the manager.
func (v *NstanceMachinePoolValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&infrastructurev1beta1.NstanceMachinePool{}).
		WithValidator(v).
		Complete()
}

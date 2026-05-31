// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NstanceMachinePoolSpec defines the desired state of NstanceMachinePool
type NstanceMachinePoolSpec struct {
	// Group is the Nstance Group key. This can match a static group in
	// server config, or be a new key for a dynamic group.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Group string `json:"group"`

	// Shards is the list of zone shards this group should be distributed across.
	// Each shard will have a corresponding NstanceShardGroup created.
	// Replicas from the MachinePool are distributed across these shards.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Shards []string `json:"shards"`

	// Template is the name of the Nstance template defined in server config.
	// Required when creating a new dynamic group (no static group with this key).
	// Must not be set for groups backed by static config (server will reject).
	// +optional
	Template string `json:"template,omitempty"`

	// InstanceType is an optional override (must be allowed by the Group)
	// +optional
	InstanceType *string `json:"instanceType,omitempty"`

	// SubnetPool specifies which subnet pool to use for new dynamic groups.
	// This is a subnet pool ID that references server.subnet_pools map key.
	// If not specified, uses the template's default.
	// Must not be set for groups backed by static config (server will reject).
	// +optional
	SubnetPool string `json:"subnetPool,omitempty"`

	// Vars are additional vars merged with Group vars (enables node labels, etc.)
	// +optional
	Vars map[string]string `json:"vars,omitempty"`

	// ProviderIDList is the list of provider IDs of running instances across all shards.
	// Required by the CAPI InfraMachinePool contract (spec.providerIDList).
	// +optional
	// +listType=atomic
	ProviderIDList []string `json:"providerIDList,omitempty"`
}

// NstanceMachinePoolStatus defines the observed state of NstanceMachinePool
type NstanceMachinePoolStatus struct {
	// Ready indicates whether the machine pool is ready
	// +optional
	Ready bool `json:"ready,omitempty"`

	// Replicas is the most recently observed number of running instances across all shards.
	// Required by the CAPI InfraMachinePool contract (status.replicas).
	// +optional
	Replicas int32 `json:"replicas"`

	// IsStatic indicates this group is backed by static server config.
	// When true, spec.template and spec.subnetPool cannot be modified.
	// +optional
	IsStatic bool `json:"isStatic,omitempty"`

	// ObservedGeneration is the generation of the spec that was last observed
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ObservedOwnerGeneration is the generation of the owner MachinePool that was last observed
	// +optional
	ObservedOwnerGeneration int64 `json:"observedOwnerGeneration,omitempty"`

	// Conditions represent the current state of the NstanceMachinePool
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=nstancemachinepools,scope=Namespaced,categories=cluster-api
// +kubebuilder:printcolumn:name="Group",type="string",JSONPath=".spec.group",description="Nstance Group"
// +kubebuilder:printcolumn:name="Static",type="boolean",JSONPath=".status.isStatic",description="Backed by static config"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready",description="Machine pool is ready"

// NstanceMachinePool is the Schema for the nstancemachinepools API
type NstanceMachinePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NstanceMachinePoolSpec   `json:"spec"`
	Status NstanceMachinePoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NstanceMachinePoolList contains a list of NstanceMachinePool
type NstanceMachinePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NstanceMachinePool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NstanceMachinePool{}, &NstanceMachinePoolList{})
}

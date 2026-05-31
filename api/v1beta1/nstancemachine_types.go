// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NstanceMachineSpec defines the desired state of NstanceMachine
type NstanceMachineSpec struct {
	// Group is the name of the Nstance Group
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Group string `json:"group"`

	// InstanceType is an optional override
	// +optional
	InstanceType *string `json:"instanceType,omitempty"`

	// Vars are additional vars
	// +optional
	Vars map[string]string `json:"vars,omitempty"`
}

// NstanceMachineStatus defines the observed state of NstanceMachine
type NstanceMachineStatus struct {
	// InstanceID is the Nstance instance ID (server-generated)
	// +optional
	InstanceID string `json:"instanceID,omitempty"`

	// Shard identifies which zone shard this instance is in
	// +optional
	Shard string `json:"shard,omitempty"`

	// ProviderID is the cloud provider instance ID
	// +optional
	ProviderID string `json:"providerID,omitempty"`

	// Ready indicates whether the instance is ready
	// +optional
	Ready bool `json:"ready,omitempty"`

	// Conditions represent the current state of the NstanceMachine
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=nstancemachines,scope=Namespaced,categories=cluster-api
// +kubebuilder:printcolumn:name="Group",type="string",JSONPath=".spec.group",description="Nstance Group"
// +kubebuilder:printcolumn:name="InstanceID",type="string",JSONPath=".status.instanceID",description="Nstance Instance ID"
// +kubebuilder:printcolumn:name="ProviderID",type="string",JSONPath=".status.providerID",description="Provider Instance ID"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready",description="Instance is ready"

// NstanceMachine is the Schema for the nstancemachines API
type NstanceMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NstanceMachineSpec   `json:"spec"`
	Status NstanceMachineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NstanceMachineList contains a list of NstanceMachine
type NstanceMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NstanceMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NstanceMachine{}, &NstanceMachineList{})
}

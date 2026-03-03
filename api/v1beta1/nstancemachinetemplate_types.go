// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NstanceMachineTemplateSpec defines the desired state of NstanceMachineTemplate
type NstanceMachineTemplateSpec struct {
	// Template defines the machines that will be created from this template
	// +required
	Template NstanceMachineTemplateResource `json:"template"`
}

// NstanceMachineTemplateResource describes the data needed to create a NstanceMachine from a template
type NstanceMachineTemplateResource struct {
	// Spec is the specification of the desired behavior of the machine
	// +required
	Spec NstanceMachineSpec `json:"spec"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nstancemachinetemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:printcolumn:name="Group",type="string",JSONPath=".spec.template.spec.group",description="Nstance Group"

// NstanceMachineTemplate is the Schema for the nstancemachinetemplates API
// This is an immutable template pattern (CAPI standard) used to stamp out Machine → NstanceMachine pairs
type NstanceMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec NstanceMachineTemplateSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// NstanceMachineTemplateList contains a list of NstanceMachineTemplate
type NstanceMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NstanceMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NstanceMachineTemplate{}, &NstanceMachineTemplateList{})
}

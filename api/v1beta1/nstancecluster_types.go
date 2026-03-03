// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NstanceClusterSpec defines the desired state of NstanceCluster.
// Nstance manages infrastructure at the pool/machine level, not the cluster
// level. NstanceCluster exists only to satisfy the CAPI contract requiring
// an infrastructure cluster ref.
type NstanceClusterSpec struct {
	// ControlPlaneEndpoint represents the endpoint for the cluster's API server.
	// Set to the management cluster's API server endpoint to satisfy the CAPI
	// infrastructure contract. Nstance does not provision control planes.
	// +optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`
}

// APIEndpoint represents a reachable Kubernetes API endpoint.
type APIEndpoint struct {
	// Host is the hostname on which the API server is serving.
	// +kubebuilder:validation:MinLength=1
	Host string `json:"host"`

	// Port is the port on which the API server is serving.
	// +kubebuilder:validation:Minimum=1
	Port int32 `json:"port"`
}

// NstanceClusterInitializationStatus provides observations of the initialization process.
type NstanceClusterInitializationStatus struct {
	// Provisioned is true when cluster infrastructure is provisioned.
	// Always true for Nstance since there is no cluster-level infrastructure.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// NstanceClusterStatus defines the observed state of NstanceCluster.
type NstanceClusterStatus struct {
	// Initialization provides observations of the NstanceCluster initialization process.
	// +optional
	Initialization NstanceClusterInitializationStatus `json:"initialization,omitempty"`

	// Conditions represent the current state of the NstanceCluster.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=nstanceclusters,scope=Namespaced,categories=cluster-api
// +kubebuilder:printcolumn:name="Provisioned",type="boolean",JSONPath=".status.initialization.provisioned",description="Cluster infrastructure is provisioned"

// NstanceCluster is the Schema for the nstanceclusters API.
// It is a stub resource that satisfies the CAPI Cluster infrastructureRef contract.
// Nstance does not require cluster-level infrastructure provisioning.
type NstanceCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NstanceClusterSpec   `json:"spec,omitempty"`
	Status NstanceClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NstanceClusterList contains a list of NstanceCluster
type NstanceClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NstanceCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NstanceCluster{}, &NstanceClusterList{})
}

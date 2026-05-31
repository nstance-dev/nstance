// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NstanceShardGroupSpec defines the desired state of NstanceShardGroup
type NstanceShardGroupSpec struct {
	// Group is the name of the Nstance Group
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Group string `json:"group"`

	// Shard identifies which zone shard this group is on
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Shard string `json:"shard"`

	// Size is the desired number of instances for THIS shard
	// +kubebuilder:validation:Minimum=0
	Size int32 `json:"size"`

	// Template is the name of the Nstance template defined in server config.
	// Required when creating a new dynamic group (no static group with this key).
	// +optional
	Template string `json:"template,omitempty"`

	// InstanceType is an optional instance type override
	// +optional
	InstanceType string `json:"instanceType,omitempty"`

	// SubnetPool specifies which subnet pool to use for new dynamic groups.
	// This is a subnet pool ID that references server.subnet_pools map key.
	// +optional
	SubnetPool string `json:"subnetPool,omitempty"`

	// Vars are additional vars merged with template vars
	// +optional
	Vars map[string]string `json:"vars,omitempty"`
}

// NstanceShardGroupConfig represents the merged configuration from the server
type NstanceShardGroupConfig struct {
	// Template is the instance template being used
	// +optional
	Template string `json:"template,omitempty"`

	// SubnetPool is the ID of the subnet pool used by this group
	// +optional
	SubnetPool string `json:"subnetPool,omitempty"`

	// InstanceType is the instance type being used
	// +optional
	InstanceType string `json:"instanceType,omitempty"`

	// Vars are the merged vars being used
	// +optional
	Vars map[string]string `json:"vars,omitempty"`
}

// NstanceShardGroupStatus defines the observed state of NstanceShardGroup
type NstanceShardGroupStatus struct {
	// ObservedGeneration is the generation most recently observed by the controller
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Replicas is the number of running instances on this shard for this group
	// +optional
	Replicas int32 `json:"replicas"`

	// ProviderIDs is the list of provider IDs of running instances on this shard (sorted)
	// +optional
	// +listType=atomic
	ProviderIDs []string `json:"providerIDs,omitempty"`

	// IsStatic indicates this group is backed by static server config on this shard.
	// When true, template and subnet pool are defined by server config and cannot be overridden.
	// +optional
	IsStatic bool `json:"isStatic,omitempty"`

	// Config is the merged configuration from the server
	// +optional
	Config *NstanceShardGroupConfig `json:"config,omitempty"`

	// LastSyncTime is when the status was last synced from the server
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the current state of the NstanceShardGroup
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for NstanceShardGroup
const (
	// ConditionTypeShardGroupReady indicates spec.size was acknowledged by server
	ConditionTypeShardGroupReady = "Ready"

	// ConditionTypeShardReachable indicates gRPC connection to shard is healthy
	ConditionTypeShardReachable = "ShardReachable"

	// ConditionTypeConfigValid indicates merged config is valid
	ConditionTypeConfigValid = "ConfigValid"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=nstanceshardgroups,scope=Namespaced,categories=cluster-api
// +kubebuilder:printcolumn:name="Group",type="string",JSONPath=".spec.group",description="Nstance Group"
// +kubebuilder:printcolumn:name="Shard",type="string",JSONPath=".spec.shard",description="Zone Shard"
// +kubebuilder:printcolumn:name="Size",type="integer",JSONPath=".spec.size",description="Desired size"
// +kubebuilder:printcolumn:name="Static",type="boolean",JSONPath=".status.isStatic",description="Backed by static config"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status",description="Shard group is ready"

// NstanceShardGroup represents a group on a single shard.
// One resource per (group, shard) pair provides per-shard visibility.
type NstanceShardGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NstanceShardGroupSpec   `json:"spec"`
	Status NstanceShardGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NstanceShardGroupList contains a list of NstanceShardGroup
type NstanceShardGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NstanceShardGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NstanceShardGroup{}, &NstanceShardGroupList{})
}

// NstanceShardGroupName returns the resource name for a NstanceShardGroup.
// Format: {group}--{shard}
func NstanceShardGroupName(group, shard string) string {
	return group + "--" + shard
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package infra

import "github.com/nstance-dev/nstance/internal/server/infra/provider"

// Provider is the infrastructure provider contract.
type Provider = provider.Provider

// ProviderConfig contains provider selection and connection settings.
type ProviderConfig = provider.ProviderConfig

// CreateInstanceRequest describes an infrastructure instance to create.
type CreateInstanceRequest = provider.CreateInstanceRequest

// CreateInstanceResponse describes a created infrastructure instance.
type CreateInstanceResponse = provider.CreateInstanceResponse

// InstanceStatus describes provider-observed instance state.
type InstanceStatus = provider.InstanceStatus

// ListInstancesRequest filters provider instance enumeration.
type ListInstancesRequest = provider.ListInstancesRequest

// ListInstancesResponse contains a page of provider instances.
type ListInstancesResponse = provider.ListInstancesResponse

// RegisterLBRequest describes a load-balancer target registration.
type RegisterLBRequest = provider.RegisterLBRequest

// DeregisterLBRequest describes a load-balancer target removal.
type DeregisterLBRequest = provider.DeregisterLBRequest

// ListLBInstancesRequest describes a load-balancer membership query.
type ListLBInstancesRequest = provider.ListLBInstancesRequest

// LoadBalancerConfig contains provider-specific load-balancer settings.
type LoadBalancerConfig = provider.LoadBalancerConfig

// LeaderNetwork describes network identity assigned to a shard leader.
type LeaderNetwork = provider.LeaderNetwork

// Re-export instance status constants.
const (
	StatusPending    = provider.StatusPending
	StatusRunning    = provider.StatusRunning
	StatusStopping   = provider.StatusStopping
	StatusStopped    = provider.StatusStopped
	StatusSuspending = provider.StatusSuspending
	StatusSuspended  = provider.StatusSuspended
	StatusDeleting   = provider.StatusDeleting
	StatusDeleted    = provider.StatusDeleted
	StatusRepairing  = provider.StatusRepairing
	StatusUnknown    = provider.StatusUnknown
)

// Re-export load-balancer target states.
const (
	LBTargetRegistered   = provider.LBTargetRegistered
	LBTargetPartial      = provider.LBTargetPartial
	LBTargetHealthy      = provider.LBTargetHealthy
	LBTargetDraining     = provider.LBTargetDraining
	LBTargetDeregistered = provider.LBTargetDeregistered
)

// Re-export helper functions
var IsUnhealthy = provider.IsUnhealthy

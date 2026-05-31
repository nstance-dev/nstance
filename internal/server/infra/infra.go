// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package infra

import "github.com/nstance-dev/nstance/internal/server/infra/provider"

// Re-export commonly used types and interfaces for convenience
type Provider = provider.Provider
type ProviderConfig = provider.ProviderConfig
type CreateInstanceRequest = provider.CreateInstanceRequest
type CreateInstanceResponse = provider.CreateInstanceResponse
type InstanceStatus = provider.InstanceStatus
type ListInstancesRequest = provider.ListInstancesRequest
type ListInstancesResponse = provider.ListInstancesResponse
type RegisterLBRequest = provider.RegisterLBRequest
type DeregisterLBRequest = provider.DeregisterLBRequest
type ListLBInstancesRequest = provider.ListLBInstancesRequest
type LoadBalancerConfig = provider.LoadBalancerConfig
type LeaderNetwork = provider.LeaderNetwork

// Re-export status constants
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

// Re-export helper functions
var IsUnhealthy = provider.IsUnhealthy

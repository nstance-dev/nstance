// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package provider

import "context"

// Provider defines the unified interface for cloud provider operations
type Provider interface {
	Kind() string

	// Instance lifecycle
	CreateInstance(ctx context.Context, req CreateInstanceRequest) (*CreateInstanceResponse, error)
	DeleteInstance(ctx context.Context, instanceID, providerInstanceID string) error
	GetInstanceStatus(ctx context.Context, instanceID, providerInstanceID string) (*InstanceStatus, error)
	ListInstances(ctx context.Context, req ListInstancesRequest) (*ListInstancesResponse, error)

	// Leader network operations (stable IP for shard leadership)
	AssignLeaderNetwork(ctx context.Context, providerInstanceID string, ln LeaderNetwork) error
	ReleaseLeaderNetwork(ctx context.Context, providerInstanceID string, ln LeaderNetwork) error

	// Networking
	CheckSubnetCapacity(ctx context.Context, subnetID string) (bool, error)

	// Load balancer groups
	RegisterWithLB(ctx context.Context, req RegisterLBRequest) error
	DeregisterFromLB(ctx context.Context, req DeregisterLBRequest) error
	ListLBInstances(ctx context.Context, req ListLBInstancesRequest) ([]string, error)
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"errors"
	"time"
)

// ErrInstanceNotFound is returned when an instance does not exist in the provider.
// This is a normal condition when instances have been externally deleted.
var ErrInstanceNotFound = errors.New("instance not found")

// CreateInstanceRequest contains parameters for creating & tagging an instance
type CreateInstanceRequest struct {
	ClusterID    string                 `json:"cluster_id"`
	Shard        string                 `json:"shard"`
	Group        string                 `json:"group"`
	Template     string                 `json:"template"`
	InstanceKind string                 `json:"instance_kind"`
	InstanceID   string                 `json:"instance_id"`
	InstanceType string                 `json:"instance_type"`
	SubnetID     string                 `json:"subnet_id"`
	UserData     string                 `json:"user_data"`
	Nonce        string                 `json:"nonce"`   // Registration nonce JWT (used by dev provider)
	CACertPEM    []byte                 `json:"ca_cert"` // CA certificate PEM (used by dev provider)
	Args         map[string]interface{} `json:"args"`

	// CustomTags are user-defined tags that providers apply as-is
	CustomTags map[string]string `json:"custom_tags"`
}

// CreateInstanceResponse contains the result of instance creation
type CreateInstanceResponse struct {
	InstanceID         string            `json:"instance_id"`
	ProviderInstanceID string            `json:"provider_instance_id"`
	Status             string            `json:"status"`
	PrivateIPv4        string            `json:"private_ipv4"`
	PrivateIPv6        string            `json:"private_ipv6"`
	Hostname           string            `json:"hostname"`
	LaunchedAt         time.Time         `json:"launched_at"`
	Tags               map[string]string `json:"tags"`
}

// InstanceStatus represents the current state of an instance
type InstanceStatus struct {
	InstanceID         string            `json:"instance_id"`
	ProviderInstanceID string            `json:"provider_instance_id"`
	Status             string            `json:"status"`
	InstanceType       string            `json:"instance_type"`
	PrivateIPv4        string            `json:"private_ipv4"`
	PrivateIPv6        string            `json:"private_ipv6"`
	Hostname           string            `json:"hostname"`
	LaunchedAt         time.Time         `json:"launched_at"`
	Tags               map[string]string `json:"tags"`
	Region             string            `json:"region"`
	Zone               string            `json:"zone"`

	// "Associations" Metadata - used for filtering/GC/reconciliation.
	// These are authoritative metadata fields that determine ownership.
	ClusterID string `json:"cluster_id"`
	Shard     string `json:"shard"`

	// "Annotations" Metadata - informational, not used for filtering or reconciliation.
	// May be nil if not available from provider (e.g. Proxmox VE requires per-VM lookup, so /cluster/resources is nil).
	Annotations *InstanceAnnotations `json:"annotations,omitempty"`
}

// InstanceAnnotations contains informational metadata about an instance.
// These are not used for filtering, GC, or reconciliation - only for display purposes.
type InstanceAnnotations struct {
	Group     string     `json:"group,omitempty"`
	Kind      string     `json:"kind,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

// GetGroup returns the group from annotations, or empty string if not available.
func (s *InstanceStatus) GetGroup() string {
	if s.Annotations != nil {
		return s.Annotations.Group
	}
	return ""
}

// GetKind returns the kind from annotations, or empty string if not available.
func (s *InstanceStatus) GetKind() string {
	if s.Annotations != nil {
		return s.Annotations.Kind
	}
	return ""
}

// ListInstancesRequest contains parameters for listing instances with pagination
type ListInstancesRequest struct {
	ClusterID string `json:"cluster_id"`
	Shard     string `json:"shard"`
	Limit     int    `json:"limit"`
	NextToken string `json:"next_token"`
}

// ListInstancesResponse contains the result of paginated instance listing
type ListInstancesResponse struct {
	Instances []*InstanceStatus `json:"instances"`
	NextToken string            `json:"next_token"`
	Total     int               `json:"total"`
}

// LoadBalancerConfig defines provider-specific load balancer configuration.
type LoadBalancerConfig struct {
	Provider string `json:"provider" validate:"required,oneof=aws google tunnel"`

	TargetGroups []AWSTargetGroupConfig `json:"target_groups,omitempty"`

	NetworkEndpointGroups []string                  `json:"network_endpoint_groups,omitempty"`
	Frontends             []GoogleNLBFrontendConfig `json:"frontends,omitempty"`

	Listeners []TunnelListenerConfig `json:"listeners,omitempty"`
}

// AWSTargetGroupConfig identifies an AWS target group and its public, production, and proxy ports.
type AWSTargetGroupConfig struct {
	ARN          string `json:"arn"`
	ListenerPort int    `json:"listener_port"`
	TargetPort   int    `json:"target_port"`
	ProxyPort    int    `json:"proxy_port"`
}

// GoogleNLBFrontendConfig identifies a Google Cloud forwarding-rule frontend.
type GoogleNLBFrontendConfig struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

// TunnelListenerConfig identifies the production and local proxy ports for a tunnel listener.
type TunnelListenerConfig struct {
	TargetPort int `json:"target_port"`
	ProxyPort  int `json:"proxy_port"`
}

// RegisterLBRequest contains parameters for registering an instance with a load balancer
type RegisterLBRequest struct {
	ProviderInstanceID string
	LBConfig           LoadBalancerConfig
	Zone               string
}

// DeregisterLBRequest contains parameters for deregistering an instance from a load balancer
type DeregisterLBRequest struct {
	ProviderInstanceID string
	LBConfig           LoadBalancerConfig
	Zone               string
}

// ListLBInstancesRequest contains parameters for listing load balancer instances
type ListLBInstancesRequest struct {
	LBConfig LoadBalancerConfig
	Zone     string
}

// ProviderConfig contains provider-specific configuration
type ProviderConfig struct {
	Kind    string                 `json:"kind"`
	Region  string                 `json:"region"`
	Zone    string                 `json:"zone"`
	Options map[string]interface{} `json:"options,omitempty"` // Provider-specific options
}

// Common instance statuses - standardized across all cloud providers
const (
	StatusPending    = "pending"    // AWS: "pending", Google Cloud: "PROVISIONING"/"STAGING", mock: on create
	StatusRunning    = "running"    // AWS: "running", Google Cloud: "RUNNING", Proxmox: "running", tmux: window alive, mock: after create
	StatusStopping   = "stopping"   // AWS: "stopping", Google Cloud: "STOPPING"
	StatusStopped    = "stopped"    // AWS: "stopped", Google Cloud: "STOPPED", Proxmox: "stopped"
	StatusSuspending = "suspending" // Google Cloud: "SUSPENDING"
	StatusSuspended  = "suspended"  // Google Cloud: "SUSPENDED", Proxmox: "paused"
	StatusDeleting   = "deleting"   // AWS: "shutting-down", mock: on delete
	StatusDeleted    = "deleted"    // AWS: "terminated", Google Cloud: "TERMINATED", tmux: window dead/missing, mock: after delete
	StatusRepairing  = "repairing"  // Google Cloud: "REPAIRING" (live migration)
	StatusUnknown    = "unknown"    // AWS/Google Cloud/Proxmox: any unrecognised state (not in IsUnhealthy, not filtered by SQL)
)

// IsUnhealthy checks if an instance status indicates it needs replacement
func IsUnhealthy(status string) bool {
	switch status {
	case StatusStopping, StatusStopped,
		StatusSuspending, StatusSuspended,
		StatusDeleting, StatusDeleted,
		StatusRepairing:
		return true
	default:
		return false
	}
}

// LeaderNetwork contains the stable network configuration for shard leadership.
// This is used to assign/release a stable IP address when acquiring/losing leadership.
type LeaderNetwork struct {
	IP          string // Stable leader IP address: AWS wait-for-routable, Google Cloud alias IP.
	InterfaceID string // Provider resource ID: ENI ID for AWS, empty for Google Cloud.
}

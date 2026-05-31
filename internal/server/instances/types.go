// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package instances

import (
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
)

// CreateInstanceRequest contains parameters for creating an instance
type CreateInstanceRequest struct {
	InstanceID   string                 `json:"instance_id"`   // If empty, will be generated
	Group        string                 `json:"group"`         // Required group reference
	Tenant       string                 `json:"tenant"`        // Tenant identifier
	Template     string                 `json:"template"`      // Template name from configuration (optional override)
	InstanceType string                 `json:"instance_type"` // Override template/group instance type
	SubnetPool   string                 `json:"subnet_pool"`   // Override template/group subnet pool
	Vars         map[string]string      `json:"vars"`          // Additional vars
	Args         map[string]interface{} `json:"args"`          // Override template/group args
	Tags         map[string]string      `json:"tags"`          // Additional instance tags
	OnDemand     bool                   `json:"on_demand"`     // Whether this is an on-demand instance (not managed by group reconciliation)
}

// CreateInstanceResponse contains the result of instance creation
type CreateInstanceResponse struct {
	InstanceID         string    `json:"instance_id"`
	Group              string    `json:"group"`
	Template           string    `json:"template"`
	ProviderInstanceID string    `json:"provider_instance_id"`
	Status             string    `json:"status"`
	PrivateIPv4        string    `json:"private_ipv4"`
	PrivateIPv6        string    `json:"private_ipv6"`
	Hostname           string    `json:"hostname"`
	CreatedAt          time.Time `json:"created_at"`
	RegistrationJWT    string    `json:"registration_jwt"`
}

// InstanceStatus represents the current state of an instance
type InstanceStatus struct {
	InstanceID         string            `json:"instance_id"`
	Group              string            `json:"group"`
	ProviderInstanceID string            `json:"provider_instance_id"`
	Status             string            `json:"status"`
	InstanceType       string            `json:"instance_type"`
	PrivateIPv4        string            `json:"private_ipv4"`
	PrivateIPv6        string            `json:"private_ipv6"`
	CreatedAt          time.Time         `json:"created_at"`
	LastUpdated        time.Time         `json:"last_updated"`
	Tags               map[string]string `json:"tags"`
}

// InstanceRecord represents the stored instance data
type InstanceRecord struct {
	InstanceID         string               `json:"instance_id"`
	Tenant             string               `json:"tenant"`
	Group              string               `json:"group"`
	OnDemand           bool                 `json:"on_demand"`
	ProviderInstanceID string               `json:"provider_instance_id"`
	InstanceType       string               `json:"instance_type"`
	Status             string               `json:"status"`
	PrivateIPv4        string               `json:"private_ipv4"`
	PrivateIPv6        string               `json:"private_ipv6"`
	Hostname           string               `json:"hostname"`
	CreatedAt          time.Time            `json:"created_at"`
	LastUpdated        time.Time            `json:"last_updated"`
	RegistrationJWT    string               `json:"registration_jwt"`
	Config             *config.MergedConfig `json:"config"` // Merged configuration used
	Tags               map[string]string    `json:"tags"`
	InfraConfigHash    string               `json:"infra_config_hash"` // Infra config hash at provision time

	// Registration data (populated after registration completes)
	RegisteredAt  *time.Time `json:"registered_at,omitempty"`
	PublicKeyPEM  string     `json:"public_key_pem,omitempty"`
	CertSerial    string     `json:"cert_serial,omitempty"`
	CertExpiresAt *time.Time `json:"cert_expires_at,omitempty"`
}

// GroupInstanceRequest represents a request to create instances for a group
type GroupInstanceRequest struct {
	Group        string                 `json:"group"`
	Template     string                 `json:"template"`
	Count        int                    `json:"count"`
	InstanceType string                 `json:"instance_type"`
	SubnetPool   string                 `json:"subnet_pool"`
	Vars         map[string]string      `json:"vars"`
	Args         map[string]interface{} `json:"args"`
	Tags         map[string]string      `json:"tags"`
}

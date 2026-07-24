// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/nstance-dev/nstance/internal/identifiers"
)

// Duration is a wrapper around time.Duration that supports parsing duration strings in JSON
type Duration time.Duration

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(dur)
	return nil
}

// Config represents the complete Nstance Server configuration
type Config struct {
	Cluster       ClusterConfig                     `json:"cluster" validate:"required"`
	Shard         ShardConfig                       `json:"shard" validate:"required"`
	LoadBalancers map[string]LoadBalancerConfig     `json:"load_balancers"`
	Images        map[string]ImageConfig            `json:"images"`
	Certificates  map[string]CertConfig             `json:"certificates"`
	Defaults      DefaultsConfig                    `json:"defaults"`
	Templates     map[string]TemplateConfig         `json:"templates" validate:"required"`
	Groups        map[string]map[string]GroupConfig `json:"groups"` // tenant -> group key -> config
}

// ClusterConfig defines cluster-scoped configuration shared across all shards.
type ClusterConfig struct {
	ID             string                `json:"id" validate:"required"`
	Storage        *ClusterStorageConfig `json:"storage,omitempty"`
	Secrets        SecretsConfig         `json:"secrets" validate:"required"`
	LeaderElection LeaderElectionConfig  `json:"leader_election"`
}

// ClusterStorageConfig defines optional separate storage for cluster-scoped data.
// If not specified, the shard bucket is used with the "cluster/" prefix.
type ClusterStorageConfig struct {
	Provider string `json:"provider" validate:"required,oneof=s3 gcs file"`
	Bucket   string `json:"bucket"`
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"` // Used for S3-compatible object stores (SeaweedFS, MinIO, Ceph RGW)
	Prefix   string `json:"prefix"`             // Default: "cluster/"
}

// LeaderElectionConfig defines leader election timing options.
// Used for both cluster and shard leader election.
type LeaderElectionConfig struct {
	Enabled            *bool    `json:"enabled,omitempty"`             // Default: true. Use pointer to distinguish unset from false
	FrequentInterval   Duration `json:"frequent_interval,omitempty"`   // Polling interval during transitions (default: 5s)
	InfrequentInterval Duration `json:"infrequent_interval,omitempty"` // Polling interval during stable periods (default: 30s)
	LeaderTimeout      Duration `json:"leader_timeout,omitempty"`      // Time before considering leader failed (default: 15s)
}

// ShardConfig defines shard-specific configuration for a single shard server.
type ShardConfig struct {
	ID                   string                  `json:"id" validate:"required"`
	Infra                InfraConfig             `json:"infra" validate:"required"`
	Bind                 BindConfig              `json:"bind" validate:"required"`
	Advertise            AdvertiseConfig         `json:"advertise" validate:"required"`
	LeaderNetwork        *LeaderNetworkConfig    `json:"leader_network,omitempty"`
	RequestTimeout       Duration                `json:"request_timeout,omitempty"`
	CreateRateLimit      Duration                `json:"create_rate_limit,omitempty"`
	HealthCheckInterval  Duration                `json:"health_check_interval,omitempty"`
	DefaultDrainTimeout  Duration                `json:"default_drain_timeout,omitempty"`
	ImageRefreshInterval Duration                `json:"image_refresh_interval,omitempty"`
	ShutdownTimeout      Duration                `json:"shutdown_timeout,omitempty"`
	SubnetPools          map[string][]string     `json:"subnet_pools"`
	DynamicSubnetPools   []string                `json:"dynamic_subnet_pools"`
	GarbageCollection    GarbageCollectionConfig `json:"garbage_collection"`
	Expiry               ExpiryConfig            `json:"expiry"`
	LeaderElection       LeaderElectionConfig    `json:"leader_election"`
	ErrorExitJitter      ErrorExitJitterConfig   `json:"error_exit_jitter"`
}

// InfraConfig defines infrastructure provider configuration for the shard.
type InfraConfig struct {
	Provider string                 `json:"provider" validate:"required,oneof=aws google mock tmux proxmox"`
	Region   string                 `json:"region" validate:"required"`
	Zone     string                 `json:"zone" validate:"required"`
	Options  map[string]interface{} `json:"options,omitempty"` // Provider-specific options
}

// IsClusterLeaderElectionEnabled returns true if cluster leader election is enabled or not set.
func (c *Config) IsClusterLeaderElectionEnabled() bool {
	if c.Cluster.LeaderElection.Enabled == nil {
		return true
	}
	return *c.Cluster.LeaderElection.Enabled
}

// IsShardLeaderElectionEnabled returns true if shard leader election is enabled or not set.
func (c *Config) IsShardLeaderElectionEnabled() bool {
	if c.Shard.LeaderElection.Enabled == nil {
		return true
	}
	return *c.Shard.LeaderElection.Enabled
}

// GetShardStoragePrefix returns the shard storage prefix in the format "shard/{shard.id}/".
func (c *Config) GetShardStoragePrefix() string {
	return "shard/" + c.Shard.ID + "/"
}

// BindConfig defines server binding configuration with per-service addresses
type BindConfig struct {
	HealthAddr       string `json:"health_addr" validate:"required,addr"`
	ElectionAddr     string `json:"election_addr" validate:"required,addr"`
	RegistrationAddr string `json:"registration_addr" validate:"required,addr"`
	OperatorAddr     string `json:"operator_addr" validate:"required,addr"`
	AgentAddr        string `json:"agent_addr" validate:"required,addr"`
}

// AdvertiseConfig defines server advertised addresses with per-service addresses
type AdvertiseConfig struct {
	HealthAddr       string `json:"health_addr" validate:"required,addr"`
	ElectionAddr     string `json:"election_addr" validate:"required,addr"`
	RegistrationAddr string `json:"registration_addr" validate:"required,addr"`
	OperatorAddr     string `json:"operator_addr" validate:"required,addr"`
	AgentAddr        string `json:"agent_addr" validate:"required,addr"`
}

// ErrorExitJitterConfig defines jittered delay timing before server exits on error
type ErrorExitJitterConfig struct {
	MinDelay Duration `json:"min_delay"` // Minimum delay before exit (default: 10s)
	MaxDelay Duration `json:"max_delay"` // Maximum delay before exit (default: 40s)
}

// LeaderNetworkConfig defines stable leader network configuration for shard leadership.
// This enables a stable IP address to be assigned when acquiring leadership.
type LeaderNetworkConfig struct {
	IP          string `json:"ip,omitempty"`           // Stable leader IP address (ENI private IP for AWS, reserved IP for Google Cloud)
	InterfaceID string `json:"interface_id,omitempty"` // Provider resource ID: ENI ID for AWS; empty for Google Cloud (which uses alias IP)
}

// SecretProviderConfig defines fields shared by secrets provider configurations.
type SecretProviderConfig struct {
	ProjectID string `json:"project_id,omitempty"` // Google Cloud project ID for Secret Manager
}

// SecretsConfig defines secrets management configuration
type SecretsConfig struct {
	SecretProviderConfig

	Provider          string                `json:"provider" validate:"required,oneof=object-storage aws-parameter-store aws-secrets-manager google-secret-manager memory"`
	Prefix            string                `json:"prefix,omitempty"`              // Prefix for secrets (S3 path or AWS name prefix)
	EncryptionKey     *EncryptionKeyConfig  `json:"encryption_key,omitempty"`      // Current key (used for encryption)
	OldEncryptionKeys []EncryptionKeyConfig `json:"old_encryption_keys,omitempty"` // Old keys (decryption only during rotation)
	CacheTTL          Duration              `json:"cache_ttl,omitempty"`           // Cache TTL for secrets, 0 disables caching
}

// EncryptionKeyConfig defines Encryption Key configuration
type EncryptionKeyConfig struct {
	SecretProviderConfig

	Provider string `json:"provider" validate:"required,oneof=env file aws-parameter-store aws-secrets-manager google-secret-manager"`
	Source   string `json:"source" validate:"required"` // Env var, file path, parameter name, secret name, or secret ARN
}

// DefaultsConfig defines global defaults
type DefaultsConfig struct {
	Args     map[string]interface{} `json:"args"`
	Vars     map[string]string      `json:"vars"`
	Userdata *UserdataConfig        `json:"userdata,omitempty"`
}

// ImageConfig defines configuration for automatic image lookup
type ImageConfig struct {
	Provider string        `json:"provider" validate:"required,oneof=aws oci"` // Cloud provider (aws, oci for Oracle Cloud)
	Filters  []ImageFilter `json:"filters" validate:"required,min=1"`          // Image filters
	Owners   []string      `json:"owners,omitempty"`                           // Image owners (AWS account IDs, "self", or "amazon")
	Sort     string        `json:"sort" validate:"required"`                   // Field to sort by (e.g., "creation-date", "name")
	Order    string        `json:"order" validate:"required,oneof=asc desc"`   // Sort order (asc, desc)
	Fallback *string       `json:"fallback,omitempty"`                         // Optional fallback image ID
}

// ImageFilter defines a filter for image lookup
type ImageFilter struct {
	Name   string   `json:"name" validate:"required"`   // Filter name (e.g., "name", "owner-id")
	Values []string `json:"values" validate:"required"` // Filter values
}

// GarbageCollectionConfig defines garbage collection scheduling options
type GarbageCollectionConfig struct {
	Interval               Duration `json:"interval,omitempty"`                 // How often to run GC (e.g., "2m"); 0 disables GC
	RegistrationTimeout    Duration `json:"registration_timeout,omitempty"`     // How long to wait for instance registration before terminating as dangling (e.g., "5m")
	DeletedRecordRetention Duration `json:"deleted_record_retention,omitempty"` // How long to keep instance records after deletion (e.g., "30m"); 0 uses default (30m)
}

// ExpiryConfig defines instance expiry configuration
type ExpiryConfig struct {
	EligibleAge Duration `json:"eligible_age,omitempty"` // Age at which managed instances become eligible for opportunistic expiry
	ForcedAge   Duration `json:"forced_age,omitempty"`   // Age at which managed instances are expired immediately
	OndemandAge Duration `json:"ondemand_age,omitempty"` // Maximum age for on-demand instances before forced expiry
}

// LoadBalancerConfig defines provider-specific load balancer configuration.
type LoadBalancerConfig struct {
	Provider string `json:"provider" validate:"required,oneof=aws google tunnel"`

	// AWS-specific fields
	TargetGroups []AWSTargetGroupConfig `json:"target_groups,omitempty"`

	// Google Cloud-specific fields
	NetworkEndpointGroups []string                  `json:"network_endpoint_groups,omitempty"`
	Frontends             []GoogleNLBFrontendConfig `json:"frontends,omitempty"`

	// Tunnel-specific fields
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

// CertConfig defines certificate template configuration
type CertConfig struct {
	Kind         string   `json:"kind" validate:"required,oneof=client server"`
	CN           *string  `json:"cn"`           // Common Name (supports templating)
	Organization []string `json:"organization"` // Organization (for client certs)
	DNS          []string `json:"dns"`          // DNS SANs (supports templating)
	IP           []string `json:"ip"`           // IP SANs (supports templating)
	URI          []string `json:"uri"`          // URI SANs (supports templating)
	Country      []string `json:"country"`      // Country (C)
	Province     []string `json:"province"`     // State/Province (ST)
	Locality     []string `json:"locality"`     // Locality (L)
	Street       []string `json:"street"`       // Street Address
	PostalCode   []string `json:"postal_code"`  // Postal Code
	TTL          int      `json:"ttl"`          // Certificate TTL in hours (default: 8760 = 1 year)
}

// KeyConfig defines key reference configuration for certificates
type KeyConfig struct {
	Source string `json:"source" validate:"required,oneof=agent"` // Where the key comes from
	Name   string `json:"name" validate:"required"`               // Name of the key
}

// FileConfig defines file configuration (secrets, certificates, or templates)
type FileConfig struct {
	Kind     string      `json:"kind" validate:"required,oneof=secret storage certificate env json string"`
	Source   string      `json:"source,omitempty"`   // For secrets: secret name
	Template interface{} `json:"template,omitempty"` // For certificates: string template name; for env/json/string: template content
	Key      *KeyConfig  `json:"key,omitempty"`      // For certificates: key reference
}

// UserdataConfig defines userdata configuration for instance templates
type UserdataConfig struct {
	Source   string `json:"source,omitempty"`   // "inline" (default) or "url"
	Encoding string `json:"encoding,omitempty"` // "plain" (default), "base64", "gzip", "base64+gzip"
	Content  string `json:"content" validate:"required"`
}

// Validate validates the UserdataConfig
func (u *UserdataConfig) Validate() error {
	if u == nil {
		return nil
	}

	// Validate source
	source := u.Source
	if source == "" {
		source = "inline"
	}
	if source != "inline" && source != "url" {
		return fmt.Errorf("invalid userdata source %q: must be 'inline' or 'url'", u.Source)
	}

	// Validate encoding
	encoding := u.Encoding
	if encoding == "" {
		encoding = "plain"
	}
	validEncodings := map[string]bool{
		"plain":       true,
		"base64":      true,
		"gzip":        true,
		"base64+gzip": true,
	}
	if !validEncodings[encoding] {
		return fmt.Errorf("invalid userdata encoding %q: must be 'plain', 'base64', 'gzip', or 'base64+gzip'", u.Encoding)
	}

	// Validate that gzip encoding is only used with url source (can't embed binary in JSON)
	if encoding == "gzip" && source == "inline" {
		return fmt.Errorf("userdata encoding 'gzip' is only valid with source 'url' (cannot embed binary in JSON)")
	}

	// Validate content is not empty
	if u.Content == "" {
		return fmt.Errorf("userdata content is required")
	}

	return nil
}

// TemplateConfig defines instance template configuration
type TemplateConfig struct {
	Kind         string                 `json:"kind" validate:"required,len=3,lowercase,alpha"`
	Arch         string                 `json:"arch" validate:"required,oneof=amd64 arm64"`
	Files        map[string]FileConfig  `json:"files"` // fileName -> FileConfig mapping
	Args         map[string]interface{} `json:"args"`
	Userdata     *UserdataConfig        `json:"userdata,omitempty"`
	Size         int                    `json:"size" validate:"min=0"`
	InstanceType string                 `json:"instance_type"`
	SubnetPool   string                 `json:"subnet_pool"`
	Vars         map[string]string      `json:"vars"`
}

// GroupConfig defines group configuration
type GroupConfig struct {
	Template      string                 `json:"template,omitempty" validate:"required,alphanum"`
	Size          *int                   `json:"size,omitempty" validate:"omitempty,min=0"` // nil = inherit from static config
	InstanceType  string                 `json:"instance_type,omitempty"`
	SubnetPool    string                 `json:"subnet_pool,omitempty"`
	Vars          map[string]string      `json:"vars,omitempty"`
	Args          map[string]interface{} `json:"args,omitempty"`
	DrainTimeout  *Duration              `json:"drain_timeout,omitempty"`  // How long to wait for drain (nil = use server default, 0 = immediate deletion)
	LoadBalancers []string               `json:"load_balancers,omitempty"` // References to load balancer keys
}

// GetSize returns the size value, defaulting to 0 if nil
func (g GroupConfig) GetSize() int {
	if g.Size == nil {
		return 0
	}
	return *g.Size
}

// IntPtr returns a pointer to an int value (helper for struct literals)
func IntPtr(i int) *int {
	return &i
}

// InstanceConfig defines individual instance configuration (for on-demand instances)
type InstanceConfig struct {
	Group        string            `json:"group" validate:"required,alphanum"` // Required group reference
	Template     string            `json:"template"`                           // Optional template override
	InstanceType string            `json:"instance_type"`                      // Optional instance type override
	SubnetPool   string            `json:"subnet_pool"`                        // Optional subnet pool override
	Vars         map[string]string `json:"vars"`                               // Additional vars
}

// MergedConfig represents a fully resolved configuration after merging
type MergedConfig struct {
	Args         map[string]interface{} `json:"args"`
	Vars         map[string]string      `json:"vars"`
	Files        map[string]FileConfig  `json:"files"`
	Userdata     *UserdataConfig        `json:"userdata,omitempty"`
	InstanceType string                 `json:"instance_type"`
	SubnetPool   string                 `json:"subnet_pool"`
	Kind         string                 `json:"kind"`
	Arch         string                 `json:"arch"`
}

// Validate validates the configuration using struct tags
func (c *Config) Validate() error {
	validate := validator.New()

	// Register custom validators
	if err := RegisterCustomValidators(validate); err != nil {
		return fmt.Errorf("failed to register custom validators: %w", err)
	}

	if err := validate.Struct(c); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}
	if err := validateSecretProviderConfig("cluster.secrets", c.Cluster.Secrets.Provider, c.Cluster.Secrets.SecretProviderConfig); err != nil {
		return err
	}
	if c.Cluster.Secrets.EncryptionKey != nil {
		key := c.Cluster.Secrets.EncryptionKey
		if err := validateSecretProviderConfig("cluster.secrets.encryption_key", key.Provider, key.SecretProviderConfig); err != nil {
			return err
		}
	}
	for i, key := range c.Cluster.Secrets.OldEncryptionKeys {
		if err := validateSecretProviderConfig(fmt.Sprintf("cluster.secrets.old_encryption_keys[%d]", i), key.Provider, key.SecretProviderConfig); err != nil {
			return err
		}
	}

	// Additional custom validations - ensure all bind addrs use unique ports when on the same host
	bindAddrs := []string{c.Shard.Bind.RegistrationAddr, c.Shard.Bind.AgentAddr, c.Shard.Bind.OperatorAddr}
	for i, addr1 := range bindAddrs {
		for j, addr2 := range bindAddrs {
			if i != j && addr1 == addr2 {
				return fmt.Errorf("bind addrs must be unique, found duplicate: %s", addr1)
			}
		}
	}

	// Validate template references in groups
	for tenant, tenantGroups := range c.Groups {
		if err := identifiers.Validate("tenant ID", tenant); err != nil {
			return fmt.Errorf("group tenant %q: %w", tenant, err)
		}
		for groupKey, group := range tenantGroups {
			if err := identifiers.Validate("group ID", groupKey); err != nil {
				return fmt.Errorf("group key %q in tenant %q: %w", groupKey, tenant, err)
			}
			if _, exists := c.Templates[group.Template]; !exists {
				return fmt.Errorf("group %s in tenant %s references unknown template: %s", tenant, groupKey, group.Template)
			}

			// Validate load balancer references
			for _, lbKey := range group.LoadBalancers {
				if _, exists := c.LoadBalancers[lbKey]; !exists {
					return fmt.Errorf("group %s in tenant %s references unknown load balancer: %s", tenant, groupKey, lbKey)
				}
			}
		}
	}

	// Validate cluster ID format and length
	if err := identifiers.Validate("cluster ID", c.Cluster.ID); err != nil {
		return fmt.Errorf("cluster.id: %w", err)
	}

	// Validate shard ID format and length
	if err := identifiers.Validate("shard ID", c.Shard.ID); err != nil {
		return fmt.Errorf("shard.id: %w", err)
	}

	// Validate cluster storage provider if set
	if c.Cluster.Storage != nil && c.Cluster.Storage.Provider != "" {
		validProviders := map[string]bool{"s3": true, "gcs": true, "file": true}
		if !validProviders[c.Cluster.Storage.Provider] {
			return fmt.Errorf("cluster.storage.provider must be one of: s3, gcs, file")
		}
	}

	// Validate load balancer configurations
	for lbKey, lbConfig := range c.LoadBalancers {
		switch lbConfig.Provider {
		case "aws":
			if len(lbConfig.TargetGroups) == 0 {
				return fmt.Errorf("load balancer %s with provider aws must specify at least one target_groups entry", lbKey)
			}
			if len(lbConfig.NetworkEndpointGroups) > 0 || len(lbConfig.Frontends) > 0 || len(lbConfig.Listeners) > 0 {
				return fmt.Errorf("load balancer %s with provider aws contains fields for another provider", lbKey)
			}
			for i, targetGroup := range lbConfig.TargetGroups {
				if targetGroup.ARN == "" {
					return fmt.Errorf("load balancer %s target_groups[%d].arn must not be empty", lbKey, i)
				}
				if err := validateLoadBalancerPort(lbKey, fmt.Sprintf("target_groups[%d].listener_port", i), targetGroup.ListenerPort); err != nil {
					return err
				}
				if err := validateLoadBalancerPort(lbKey, fmt.Sprintf("target_groups[%d].target_port", i), targetGroup.TargetPort); err != nil {
					return err
				}
				if err := validateLoadBalancerPort(lbKey, fmt.Sprintf("target_groups[%d].proxy_port", i), targetGroup.ProxyPort); err != nil {
					return err
				}
			}
		case "google":
			if len(lbConfig.NetworkEndpointGroups) == 0 {
				return fmt.Errorf("load balancer %s with provider google must specify at least one network_endpoint_groups entry", lbKey)
			}
			if len(lbConfig.Frontends) == 0 {
				return fmt.Errorf("load balancer %s with provider google must specify at least one frontends entry", lbKey)
			}
			if len(lbConfig.TargetGroups) > 0 || len(lbConfig.Listeners) > 0 {
				return fmt.Errorf("load balancer %s with provider google contains fields for another provider", lbKey)
			}
			for i, name := range lbConfig.NetworkEndpointGroups {
				if name == "" {
					return fmt.Errorf("load balancer %s has empty network_endpoint_groups entry at index %d", lbKey, i)
				}
			}
			for i, frontend := range lbConfig.Frontends {
				if _, err := netip.ParseAddr(frontend.IP); err != nil {
					return fmt.Errorf("load balancer %s frontends[%d].ip must be a valid IP address", lbKey, i)
				}
				if err := validateLoadBalancerPort(lbKey, fmt.Sprintf("frontends[%d].port", i), frontend.Port); err != nil {
					return err
				}
			}
		case "tunnel":
			if len(lbConfig.Listeners) == 0 {
				return fmt.Errorf("load balancer %s with provider tunnel must specify at least one listeners entry", lbKey)
			}
			if len(lbConfig.TargetGroups) > 0 || len(lbConfig.NetworkEndpointGroups) > 0 || len(lbConfig.Frontends) > 0 {
				return fmt.Errorf("load balancer %s with provider tunnel contains fields for another provider", lbKey)
			}
			for i, listener := range lbConfig.Listeners {
				if err := validateLoadBalancerPort(lbKey, fmt.Sprintf("listeners[%d].target_port", i), listener.TargetPort); err != nil {
					return err
				}
				if err := validateLoadBalancerPort(lbKey, fmt.Sprintf("listeners[%d].proxy_port", i), listener.ProxyPort); err != nil {
					return err
				}
			}
		}
	}

	// Validate operator and agent certificate TTLs if specified
	for certName, certConfig := range c.Certificates {
		if certName == "operator" || certName == "agent" {
			if certConfig.TTL < 0 {
				return fmt.Errorf("certificates.%s.ttl must be > 0 if specified", certName)
			}
		}
	}

	// Validate userdata in defaults
	if err := c.Defaults.Userdata.Validate(); err != nil {
		return fmt.Errorf("defaults userdata: %w", err)
	}

	// Validate file references in templates
	for templateName, template := range c.Templates {
		// Validate userdata configuration
		if err := template.Userdata.Validate(); err != nil {
			return fmt.Errorf("template %s userdata: %w", templateName, err)
		}

		for fileName, fileConfig := range template.Files {
			switch fileConfig.Kind {
			case "certificate":
				if fileConfig.Template == nil {
					return fmt.Errorf("template %s file %s of kind certificate must specify a template", templateName, fileName)
				}
				templateName, ok := fileConfig.Template.(string)
				if !ok {
					return fmt.Errorf("template %s file %s of kind certificate must have string template", templateName, fileName)
				}
				if _, exists := c.Certificates[templateName]; !exists {
					return fmt.Errorf("template %s file %s references unknown certificate template %s", templateName, fileName, templateName)
				}
			case "secret":
				if fileConfig.Source == "" {
					return fmt.Errorf("template %s file %s of kind secret must specify a source", templateName, fileName)
				}
			case "storage":
				if fileConfig.Source == "" {
					return fmt.Errorf("template %s file %s of kind storage must specify a source", templateName, fileName)
				}
			case "env":
				if fileConfig.Template == nil {
					return fmt.Errorf("template %s file %s of kind env must specify a template", templateName, fileName)
				}
				// Template should be map[string]interface{} with string values for env files
				templateObj, ok := fileConfig.Template.(map[string]interface{})
				if !ok {
					return fmt.Errorf("template %s file %s of kind env must have object template", templateName, fileName)
				}
				// Validate all values are strings for env files
				for key, value := range templateObj {
					if _, ok := value.(string); !ok {
						return fmt.Errorf("template %s file %s env key %s must have string value", templateName, fileName, key)
					}
				}
			case "json":
				if fileConfig.Template == nil {
					return fmt.Errorf("template %s file %s of kind json must specify a template", templateName, fileName)
				}
				// Template should be an object for JSON files
				if _, ok := fileConfig.Template.(map[string]interface{}); !ok {
					return fmt.Errorf("template %s file %s of kind json must have object template", templateName, fileName)
				}
			case "string":
				if fileConfig.Template == nil {
					return fmt.Errorf("template %s file %s of kind string must specify a template", templateName, fileName)
				}
				// Template should be a string for string files
				if _, ok := fileConfig.Template.(string); !ok {
					return fmt.Errorf("template %s file %s of kind string must have string template", templateName, fileName)
				}
			}
		}
	}

	// Validate garbage collection settings
	gcInterval := c.Shard.GarbageCollection.Interval
	gcRegTimeout := c.Shard.GarbageCollection.RegistrationTimeout
	if gcInterval < 0 {
		return fmt.Errorf("shard.garbage_collection.interval must be >= 0")
	}
	if gcRegTimeout < 0 {
		return fmt.Errorf("shard.garbage_collection.registration_timeout must be >= 0")
	}
	if (gcInterval == 0) != (gcRegTimeout == 0) {
		return fmt.Errorf("shard.garbage_collection.interval and registration_timeout must both be zero to disable GC")
	}
	if gcInterval > 0 && gcRegTimeout <= gcInterval {
		return fmt.Errorf("shard.garbage_collection.registration_timeout must be greater than interval")
	}

	// Validate expiry settings
	expiry := c.Shard.Expiry
	if expiry.EligibleAge < 0 {
		return fmt.Errorf("shard.expiry.eligible_age must be >= 0")
	}
	if expiry.ForcedAge < 0 {
		return fmt.Errorf("shard.expiry.forced_age must be >= 0")
	}
	if expiry.OndemandAge < 0 {
		return fmt.Errorf("shard.expiry.ondemand_age must be >= 0")
	}
	if expiry.EligibleAge > 0 && expiry.ForcedAge > 0 && expiry.EligibleAge >= expiry.ForcedAge {
		return fmt.Errorf("shard.expiry.eligible_age must be less than forced_age when both are set")
	}

	// Validate encryption key is required for object-storage secrets provider, and invalid for other providers
	if c.Cluster.Secrets.Provider == "object-storage" && c.Cluster.Secrets.EncryptionKey == nil {
		return fmt.Errorf("encryption_key is required for object-storage secrets provider")
	}
	if c.Cluster.Secrets.EncryptionKey != nil && c.Cluster.Secrets.Provider != "object-storage" {
		return fmt.Errorf("encryption_key is only valid for object-storage secrets provider")
	}

	// Validate image configurations
	for imageName, imageConfig := range c.Images {
		// Validate image name uses valid Go template identifiers (alphanumeric + underscore)
		if !isValidTemplateIdentifier(imageName) {
			return fmt.Errorf("image name %s must use only alphanumeric characters and underscores (valid Go template identifier)", imageName)
		}

		// Validate provider matches shard infra provider (or is compatible)
		// e.g. Allow "aws" image config when provider is "aws"
		if imageConfig.Provider != c.Shard.Infra.Provider {
			return fmt.Errorf("image %s provider %s does not match shard.infra.provider %s", imageName, imageConfig.Provider, c.Shard.Infra.Provider)
		}

		// Validate sort field
		validSortFields := map[string]bool{
			"creation-date": true,
			"name":          true,
		}
		if !validSortFields[imageConfig.Sort] {
			return fmt.Errorf("image %s has invalid sort field %s (must be 'creation-date' or 'name')", imageName, imageConfig.Sort)
		}

		// Validate filters are not empty
		if len(imageConfig.Filters) == 0 {
			return fmt.Errorf("image %s must have at least one filter", imageName)
		}
	}

	// Validate subnet configuration
	if err := c.ValidateSubnetConfig(); err != nil {
		return err
	}

	// Provider-specific validation
	if err := c.validateProviderArgs(); err != nil {
		return err
	}

	return nil
}

// validateLoadBalancerPort validates that port is in the TCP port number range.
func validateLoadBalancerPort(lbKey, field string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("load balancer %s %s must be a valid TCP port (1..65535)", lbKey, field)
	}
	return nil
}

// validateProviderArgs validates provider-specific args are present in defaults/templates
func (c *Config) validateProviderArgs() error {
	// Validate leader network configuration if present
	if c.Shard.LeaderNetwork != nil {
		if err := c.validateLeaderNetwork(); err != nil {
			return err
		}
	}

	switch c.Shard.Infra.Provider {
	case "proxmox":
		return c.validateProxmoxArgs()
	}
	return nil
}

// validateLeaderNetwork validates provider-specific leader network requirements
func (c *Config) validateLeaderNetwork() error {
	if c.Shard.LeaderNetwork.IP == "" {
		return fmt.Errorf("shard.leader_network.ip is required when leader_network is configured")
	}

	switch c.Shard.Infra.Provider {
	case "aws":
		if c.Shard.LeaderNetwork.InterfaceID == "" {
			return fmt.Errorf("shard.leader_network.interface_id is required for AWS provider (ENI ID)")
		}
	}

	return nil
}

// validateProxmoxArgs ensures required Proxmox args are present in merged config
func (c *Config) validateProxmoxArgs() error {
	for tenant, tenantGroups := range c.Groups {
		for groupName, group := range tenantGroups {
			templateName := group.Template
			templateConfig, exists := c.Templates[templateName]
			if !exists {
				continue
			}

			mergedArgs := make(map[string]interface{})
			for k, v := range c.Defaults.Args {
				mergedArgs[k] = v
			}
			for k, v := range templateConfig.Args {
				mergedArgs[k] = v
			}
			for k, v := range group.Args {
				mergedArgs[k] = v
			}

			storagePool, _ := mergedArgs["StoragePool"].(string)
			templateVMID, hasVMID := mergedArgs["TemplateVMID"].(float64)
			templateNameArg, hasName := mergedArgs["TemplateName"].(string)

			groupRef := fmt.Sprintf("%s in tenant %s", groupName, tenant)
			if storagePool == "" {
				return fmt.Errorf("group %s: StoragePool is required in args for proxmox provider", groupRef)
			}
			if hasVMID && templateVMID != 0 && hasName && templateNameArg != "" {
				return fmt.Errorf("group %s: TemplateVMID and TemplateName are mutually exclusive", groupRef)
			}
			if (!hasVMID || templateVMID == 0) && (!hasName || templateNameArg == "") {
				return fmt.Errorf("group %s: either TemplateVMID or TemplateName is required in args for proxmox provider", groupRef)
			}
		}
	}
	return nil
}

func validateSecretProviderConfig(path, provider string, cfg SecretProviderConfig) error {
	if provider == "google-secret-manager" && cfg.ProjectID == "" {
		return fmt.Errorf("%s.project_id is required for google-secret-manager", path)
	}
	if provider != "google-secret-manager" && cfg.ProjectID != "" {
		return fmt.Errorf("%s.project_id is only valid for google-secret-manager", path)
	}
	return nil
}

// isValidTemplateIdentifier checks if a string is a valid Go template identifier
// (alphanumeric characters and underscores only, starting with letter or underscore)
func isValidTemplateIdentifier(s string) bool {
	if len(s) == 0 {
		return false
	}

	// Must start with letter or underscore
	first := s[0]
	if (first < 'a' || first > 'z') && (first < 'A' || first > 'Z') && first != '_' {
		return false
	}

	// Rest can be alphanumeric or underscore
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}

	return true
}

// SetDefaults sets default values for optional fields
func (c *Config) SetDefaults() {
	// AWS defaults to Systems Manager Parameter Store for secrets.
	if c.Cluster.Secrets.Provider == "" && c.Shard.Infra.Provider == "aws" {
		c.Cluster.Secrets.Provider = "aws-parameter-store"
	}
	if c.Cluster.Secrets.Provider == "aws-parameter-store" && c.Cluster.Secrets.Prefix == "" {
		c.Cluster.Secrets.Prefix = "/nstance/"
	}
	if c.Cluster.Secrets.Provider == "aws-secrets-manager" && c.Cluster.Secrets.Prefix == "" {
		c.Cluster.Secrets.Prefix = "nstance/"
	}
	// Google Cloud defaults to Secret Manager for secrets.
	if c.Cluster.Secrets.Provider == "" && c.Shard.Infra.Provider == "google" {
		c.Cluster.Secrets.Provider = "google-secret-manager"
	}
	if c.Cluster.Secrets.Provider == "google-secret-manager" && c.Cluster.Secrets.Prefix == "" {
		c.Cluster.Secrets.Prefix = "nstance-"
	}

	// Cluster leader election defaults
	if c.Cluster.LeaderElection.FrequentInterval == 0 {
		c.Cluster.LeaderElection.FrequentInterval = Duration(5 * time.Second)
	}
	if c.Cluster.LeaderElection.InfrequentInterval == 0 {
		c.Cluster.LeaderElection.InfrequentInterval = Duration(30 * time.Second)
	}
	if c.Cluster.LeaderElection.LeaderTimeout == 0 {
		c.Cluster.LeaderElection.LeaderTimeout = Duration(15 * time.Second)
	}

	// Cluster storage prefix default
	if c.Cluster.Storage != nil && c.Cluster.Storage.Prefix == "" {
		c.Cluster.Storage.Prefix = "cluster/"
	}

	// Shard defaults
	if c.Shard.RequestTimeout == 0 {
		c.Shard.RequestTimeout = Duration(30 * time.Second)
	}
	if c.Shard.HealthCheckInterval == 0 {
		c.Shard.HealthCheckInterval = Duration(60 * time.Second)
	}
	if c.Shard.DefaultDrainTimeout == 0 {
		c.Shard.DefaultDrainTimeout = Duration(5 * time.Minute)
	}
	if c.Shard.ImageRefreshInterval == 0 {
		c.Shard.ImageRefreshInterval = Duration(6 * time.Hour)
	}
	if c.Shard.ShutdownTimeout == 0 {
		c.Shard.ShutdownTimeout = Duration(10 * time.Second)
	}
	if c.Shard.GarbageCollection.Interval == 0 {
		c.Shard.GarbageCollection.Interval = Duration(2 * time.Minute)
	}
	if c.Shard.GarbageCollection.RegistrationTimeout == 0 {
		c.Shard.GarbageCollection.RegistrationTimeout = Duration(5 * time.Minute)
	}

	// Shard leader election defaults
	if c.Shard.LeaderElection.FrequentInterval == 0 {
		c.Shard.LeaderElection.FrequentInterval = Duration(5 * time.Second)
	}
	if c.Shard.LeaderElection.InfrequentInterval == 0 {
		c.Shard.LeaderElection.InfrequentInterval = Duration(30 * time.Second)
	}
	if c.Shard.LeaderElection.LeaderTimeout == 0 {
		c.Shard.LeaderElection.LeaderTimeout = Duration(15 * time.Second)
	}

	// Certificate defaults
	for key, cert := range c.Certificates {
		if cert.TTL == 0 {
			cert.TTL = 8760 // 1 year in hours
			c.Certificates[key] = cert
		}
	}

	// Template defaults
	for key, template := range c.Templates {
		if template.Size == 0 {
			template.Size = 1
		}
		if template.Vars == nil {
			template.Vars = make(map[string]string)
		}
		if template.Args == nil {
			template.Args = make(map[string]interface{})
		}
		if template.Files == nil {
			template.Files = make(map[string]FileConfig)
		}
		c.Templates[key] = template
	}

	// Group defaults
	for tenant, tenantGroups := range c.Groups {
		for key, group := range tenantGroups {
			if group.Vars == nil {
				group.Vars = make(map[string]string)
			}
			if group.Args == nil {
				group.Args = make(map[string]interface{})
			}
			c.Groups[tenant][key] = group
		}
	}

	// Defaults initialization
	if c.Defaults.Args == nil {
		c.Defaults.Args = make(map[string]interface{})
	}
	if c.Defaults.Vars == nil {
		c.Defaults.Vars = make(map[string]string)
	}
}

// Clone creates a deep copy of the configuration
func (c *Config) Clone() (*Config, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config for cloning: %w", err)
	}

	var clone Config
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config clone: %w", err)
	}

	return &clone, nil
}

// GetMergedConfig resolves and merges configuration for a specific template/group combination
func (c *Config) GetMergedConfig(template string, groupConfig GroupConfig) (*MergedConfig, error) {
	templateConfig, exists := c.Templates[template]
	if !exists {
		return nil, fmt.Errorf("template not found: %s", template)
	}

	merged := &MergedConfig{
		Kind:     templateConfig.Kind,
		Arch:     templateConfig.Arch,
		Args:     make(map[string]interface{}),
		Vars:     make(map[string]string),
		Files:    templateConfig.Files,
		Userdata: templateConfig.Userdata,
	}

	// Merge args (Defaults -> Template -> Group)
	deepMergeArgs(merged.Args, c.Defaults.Args)
	deepMergeArgs(merged.Args, templateConfig.Args)
	deepMergeArgs(merged.Args, groupConfig.Args)

	// Merge vars (Defaults -> Template -> Group)
	for k, v := range c.Defaults.Vars {
		merged.Vars[k] = v
	}
	for k, v := range templateConfig.Vars {
		merged.Vars[k] = v
	}
	for k, v := range groupConfig.Vars {
		merged.Vars[k] = v
	}

	// Determine instance type (Template default -> Group override)
	merged.InstanceType = templateConfig.InstanceType
	if groupConfig.InstanceType != "" {
		merged.InstanceType = groupConfig.InstanceType
	}

	// Determine subnet pool (Template default -> Group override)
	merged.SubnetPool = templateConfig.SubnetPool
	if groupConfig.SubnetPool != "" {
		merged.SubnetPool = groupConfig.SubnetPool
	}

	// Use defaults userdata if template doesn't specify one
	if merged.Userdata == nil {
		merged.Userdata = c.Defaults.Userdata
	}

	return merged, nil
}

// deepMergeArgs merges args using the recursive merge strategy described in the spec
func deepMergeArgs(dst, src map[string]interface{}) {
	for k, v := range src {
		if vMap, ok := v.(map[string]interface{}); ok {
			if dMap, ok := dst[k].(map[string]interface{}); ok {
				deepMergeArgs(dMap, vMap)
				continue
			}
		}
		dst[k] = v
	}
}

// ConfigMetadata contains metadata about a configuration file
type ConfigMetadata struct {
	ETag         string    `json:"etag"`
	LastModified time.Time `json:"last_modified"`
	Size         int64     `json:"size"`
}

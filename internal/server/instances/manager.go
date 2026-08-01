// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nstance-dev/nstance/internal/server/infra"
	"github.com/nstance-dev/nstance/internal/server/infra/provider"

	"github.com/puidv7/puidv7-go"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/keys"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// ErrInstanceTenantMismatch indicates that an instance belongs to another tenant.
var ErrInstanceTenantMismatch = errors.New("instance belongs to another tenant")

// ImageGetter provides access to resolved image IDs
type ImageGetter interface {
	GetAll() map[string]string
}

// Manager handles instance lifecycle management
type Manager struct {
	configLoader *config.Loader
	secretsStore secrets.Store
	storage      storage.Storage
	localDB      *localdb.DB
	provider     infra.Provider
	jwtSigner    *JWTSigner
	imageGetter  ImageGetter
	caCert       []byte
	logger       *slog.Logger
	lbMu         sync.Mutex
}

// ManagerOptions contains options for creating an instance manager
type ManagerOptions struct {
	ConfigLoader *config.Loader
	SecretsStore secrets.Store
	Storage      storage.Storage
	LocalDB      *localdb.DB
	Provider     infra.Provider
	ImageGetter  ImageGetter // Optional: can be nil if no images configured
	CACert       []byte      // PEM-encoded CA certificate
	Logger       *slog.Logger
}

// NewManager creates a new instance manager
func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.ConfigLoader == nil {
		return nil, fmt.Errorf("config loader is required")
	}
	if opts.SecretsStore == nil {
		return nil, fmt.Errorf("secrets store is required")
	}
	if opts.Storage == nil {
		return nil, fmt.Errorf("storage is required")
	}
	if opts.LocalDB == nil {
		return nil, fmt.Errorf("local database is required")
	}
	if opts.Provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	if len(opts.CACert) == 0 {
		return nil, fmt.Errorf("CA certificate is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	manager := &Manager{
		configLoader: opts.ConfigLoader,
		secretsStore: opts.SecretsStore,
		storage:      opts.Storage,
		localDB:      opts.LocalDB,
		provider:     opts.Provider,
		imageGetter:  opts.ImageGetter,
		caCert:       opts.CACert,
		logger:       opts.Logger,
	}

	// Initialize JWT signer for registration nonces
	if err := manager.initialize(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to initialize instance manager: %w", err)
	}

	return manager, nil
}

// initialize loads secrets and sets up JWT signing
func (m *Manager) initialize(ctx context.Context) error {
	// Load registration nonce private key for JWT signing
	nonceKeyData, err := m.secretsStore.Get(ctx, "registration-nonce.key")
	if err != nil {
		return fmt.Errorf("failed to load registration nonce key: %w", err)
	}

	// Parse the private key
	noncePrivateKey, err := keys.ParseEd25519PrivateKey(nonceKeyData)
	if err != nil {
		return fmt.Errorf("failed to parse registration nonce private key: %w", err)
	}

	m.jwtSigner = NewJWTSigner(noncePrivateKey)
	return nil
}

// CreateInstance creates a new instance using the specified group and configuration
func (m *Manager) CreateInstance(ctx context.Context, req CreateInstanceRequest) (*CreateInstanceResponse, error) {
	// Validate required group reference and tenant is specified
	if req.Group == "" {
		return nil, fmt.Errorf("group is required for on-demand instances")
	}
	if req.Tenant == "" {
		return nil, fmt.Errorf("tenant is required")
	}

	// Get current configuration
	currentConfig := m.configLoader.GetCurrent()
	if currentConfig == nil {
		return nil, fmt.Errorf("no configuration available")
	}

	// Validate that the group exists (check merged static + dynamic groups)
	groupConfigPtr, err := config.GetGroup(ctx, m.configLoader, req.Tenant, req.Group)
	if err != nil {
		return nil, fmt.Errorf("group not found: %s", req.Group)
	}
	groupConfig := *groupConfigPtr

	// Use group's template if no override provided
	templateName := groupConfig.Template
	if req.Template != "" {
		templateName = req.Template
	}

	// Validate template exists
	templateConfig, exists := currentConfig.Templates[templateName]
	if !exists {
		return nil, fmt.Errorf("template not found: %s", templateName)
	}

	// Generate instance ID if not provided
	if req.InstanceID == "" {
		var err error
		req.InstanceID, err = puidv7.New(templateConfig.Kind)
		if err != nil {
			return nil, fmt.Errorf("failed to generate instance ID: %w", err)
		}
	}

	m.logger.Info("Creating instance",
		"instance_id", req.InstanceID,
		"group", req.Group,
		"template", templateName,
		"instance_type", req.InstanceType)

	// Create a group config for merging with overrides
	finalGroupConfig := config.GroupConfig{
		Template:     templateName,
		Size:         config.IntPtr(1),         // Single instance
		InstanceType: groupConfig.InstanceType, // Start with group defaults
		SubnetPool:   groupConfig.SubnetPool,
		Vars:         make(map[string]string),
		Args:         make(map[string]interface{}),
	}

	// Copy group vars and args
	for k, v := range groupConfig.Vars {
		finalGroupConfig.Vars[k] = v
	}
	for k, v := range groupConfig.Args {
		finalGroupConfig.Args[k] = v
	}

	// Apply instance-level overrides
	if req.InstanceType != "" {
		finalGroupConfig.InstanceType = req.InstanceType
	}
	if req.SubnetPool != "" {
		finalGroupConfig.SubnetPool = req.SubnetPool
	}
	for k, v := range req.Vars {
		finalGroupConfig.Vars[k] = v
	}
	for k, v := range req.Args {
		finalGroupConfig.Args[k] = v
	}

	mergedConfig, err := currentConfig.GetMergedConfig(templateName, finalGroupConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to merge instance configuration: %w", err)
	}

	// Generate registration nonce JWT for this instance
	// Note: Nonce is stored in SQLite and provided in userdata for agent to use during registration
	// JWT expiry matches registrationTimeout so GC won't terminate instances with valid JWTs
	jwtExpiry := currentConfig.Shard.GarbageCollection.RegistrationTimeout.Duration()
	registrationJWT, err := m.generateRegistrationNonce(ctx, req.InstanceID, "agent", req.Group, req.Tenant, currentConfig.Cluster.ID, currentConfig.Shard.ID, req.OnDemand, jwtExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to generate registration nonce: %w", err)
	}

	// Build template data (used for both userdata and args processing)
	var images map[string]string
	if m.imageGetter != nil {
		images = m.imageGetter.GetAll()
	} else {
		images = make(map[string]string)
	}
	templateData := UserdataTemplateData{
		Cluster: ClusterData{
			ID:     currentConfig.Cluster.ID,
			CACert: string(m.caCert),
		},
		Server: ServerData{
			Shard:            currentConfig.Shard.ID,
			RegistrationAddr: currentConfig.Shard.Advertise.RegistrationAddr,
			AgentAddr:        currentConfig.Shard.Advertise.AgentAddr,
			OperatorAddr:     currentConfig.Shard.Advertise.OperatorAddr,
		},
		Provider: ProviderData{
			Kind:   currentConfig.Shard.Infra.Provider,
			Region: currentConfig.Shard.Infra.Region,
			Zone:   currentConfig.Shard.Infra.Zone,
		},
		Instance: InstanceData{
			ID:   req.InstanceID,
			Kind: templateConfig.Kind,
			Arch: mergedConfig.Arch,
			Type: mergedConfig.InstanceType,
		},
		Vars:  mergedConfig.Vars,
		Image: images,
		Nonce: registrationJWT,
	}

	// Process userdata template
	userdata, err := m.processUserdataTemplate(mergedConfig.Userdata, templateData)
	if err != nil {
		return nil, fmt.Errorf("failed to process userdata template: %w", err)
	}

	// Process Args to interpolate template variables
	processedArgs, err := m.processArgs(mergedConfig.Args, templateData)
	if err != nil {
		return nil, fmt.Errorf("failed to process args: %w", err)
	}

	// Determine subnet pool to use (request overrides merged config if set)
	subnetPool := mergedConfig.SubnetPool
	if req.SubnetPool != "" {
		subnetPool = req.SubnetPool
	}
	if subnetPool == "" {
		return nil, fmt.Errorf("no subnet pool specified for instance")
	}

	// Select a subnet with available capacity
	subnetID, selectedSubnetKey, err := m.selectSubnetWithCapacity(ctx, currentConfig, subnetPool)
	if err != nil {
		return nil, fmt.Errorf("failed to select subnet: %w", err)
	}
	m.logger.Info("Selected subnet for instance",
		"instance_id", req.InstanceID,
		"subnet_key", selectedSubnetKey,
		"subnet_id", subnetID)

	// Create provider request with metadata for tags/labels
	providerReq := infra.CreateInstanceRequest{
		ClusterID:    currentConfig.Cluster.ID,
		Shard:        currentConfig.Shard.ID,
		Group:        req.Group,
		Template:     templateName,
		InstanceKind: templateConfig.Kind,
		InstanceID:   req.InstanceID,
		InstanceType: mergedConfig.InstanceType,
		SubnetID:     subnetID,
		UserData:     userdata,
		Nonce:        registrationJWT,
		CACertPEM:    m.caCert,
		Args:         processedArgs,
		CustomTags:   req.Tags,
	}

	// Compute infra config hash for this instance
	infraHash := config.HashInfraConfig(*mergedConfig)

	// Pre-insert SQLite record before provider call to prevent GC race condition.
	// GC's backfill will see this record and skip/merge rather than creating a
	// competing record with a placeholder nonce.
	now := time.Now().UTC()
	preInsert := &localdb.Instance{
		ID:              req.InstanceID,
		Tenant:          req.Tenant,
		Group:           req.Group,
		OnDemand:        req.OnDemand,
		Nonce:           registrationJWT,
		IssuedAt:        &now,
		InfraConfigHash: &infraHash,
		CreatedAt:       now,
		// ProviderID intentionally nil - not yet created
	}
	if err := m.localDB.CreateInstance(preInsert); err != nil {
		return nil, fmt.Errorf("failed to pre-insert instance record: %w", err)
	}
	m.logger.Info("Pre-inserted instance record to SQLite",
		"instance_id", req.InstanceID,
		"tenant", req.Tenant,
		"group", req.Group)

	// Store "pending" instance record in S3 BEFORE provider call.
	// This ensures registration can always find the S3 record even if server
	// crashes after provider call but before S3 write.
	// ProviderInstanceID is intentionally empty - not known until provider responds.
	instanceRecord := &InstanceRecord{
		InstanceID:      req.InstanceID,
		Tenant:          req.Tenant,
		Group:           req.Group,
		OnDemand:        req.OnDemand,
		InstanceType:    mergedConfig.InstanceType,
		Status:          "pending",
		CreatedAt:       now,
		LastUpdated:     now,
		RegistrationJWT: registrationJWT,
		Config:          mergedConfig,
		Tags:            providerReq.CustomTags,
		InfraConfigHash: infraHash,
		// ProviderInstanceID intentionally empty - not yet created
		// PrivateIPv4, PrivateIPv6 intentionally empty - not known yet
	}

	if err := m.storeInstanceRecord(ctx, instanceRecord); err != nil {
		return nil, fmt.Errorf("failed to store pending instance record: %w", err)
	}
	m.logger.Info("Stored pending instance record to S3",
		"instance_id", req.InstanceID,
		"group", req.Group)

	// Create instance via provider
	providerResp, err := m.provider.CreateInstance(ctx, providerReq)
	if err != nil {
		m.logger.Error("Failed to create instance via provider",
			"instance_id", req.InstanceID,
			"error", err)
		// S3 "pending" record exists - GC will clean up
		return nil, fmt.Errorf("failed to create instance: %w", err)
	}

	// Validate provider instance ID is present
	if providerResp.ProviderInstanceID == "" {
		m.logger.Error("Provider returned empty instance ID",
			"instance_id", req.InstanceID)
		return nil, fmt.Errorf("provider returned empty instance ID for %s", req.InstanceID)
	}

	// Write provider instance ID to S3 immediately so that if this nstance-server crashes before
	// agent registration, this or another nstance-server can match and even clean up the instance.
	instanceRecord.ProviderInstanceID = providerResp.ProviderInstanceID
	instanceRecord.PrivateIPv4 = providerResp.PrivateIPv4
	instanceRecord.PrivateIPv6 = providerResp.PrivateIPv6
	instanceRecord.Hostname = providerResp.Hostname
	instanceRecord.LastUpdated = time.Now().UTC()
	if err := m.storeInstanceRecord(ctx, instanceRecord); err != nil {
		m.logger.Warn("Failed to update S3 instance record with provider_id",
			"instance_id", req.InstanceID,
			"error", err)
		// Continue - provider_id will be backfilled by seedFromProvider on restart
	}

	// Update the pre-inserted SQLite record with provider data.
	providerAt := time.Now().UTC()
	var hostname *string
	if providerResp.Hostname != "" {
		hostname = &providerResp.Hostname
	}
	dbInstance := &localdb.Instance{
		ID:         req.InstanceID,
		Group:      req.Group,
		OnDemand:   req.OnDemand,
		ProviderID: &providerResp.ProviderInstanceID,
		ProviderAt: &providerAt,
		Hostname:   hostname,
		IP4:        &providerResp.PrivateIPv4,
		IP6:        &providerResp.PrivateIPv6,
		// Note: Nonce, IssuedAt, InfraConfigHash, CreatedAt are not updated by UpdateInstance -
		// they were set in the pre-insert and are preserved
	}

	if err := m.localDB.UpdateInstance(dbInstance); err != nil {
		return nil, fmt.Errorf("failed to update instance in local DB: %w", err)
	}

	m.logger.Info("Instance created successfully",
		"instance_id", req.InstanceID,
		"provider_instance_id", providerResp.ProviderInstanceID,
		"status", providerResp.Status)

	return &CreateInstanceResponse{
		InstanceID:         req.InstanceID,
		Group:              req.Group,
		ProviderInstanceID: providerResp.ProviderInstanceID,
		Status:             providerResp.Status,
		PrivateIPv4:        providerResp.PrivateIPv4,
		PrivateIPv6:        providerResp.PrivateIPv6,
		Hostname:           providerResp.Hostname,
		CreatedAt:          providerResp.LaunchedAt,
		RegistrationJWT:    registrationJWT,
	}, nil
}

// DeleteInstance deletes a tenant-owned instance.
func (m *Manager) DeleteInstance(ctx context.Context, tenant, instanceID string) error {
	instance, err := m.localDB.GetInstance(instanceID)
	if err != nil {
		return fmt.Errorf("instance not found in local DB: %w", err)
	}
	if instance.Tenant != tenant {
		return ErrInstanceTenantMismatch
	}
	m.logger.Info("Deleting instance", "instance_id", instanceID)

	if instance.ProviderID == nil || *instance.ProviderID == "" {
		return fmt.Errorf("instance has no provider ID")
	}
	if instance.DrainStartedAt == nil {
		if err := m.localDB.MarkDrainStarted(instanceID); err != nil {
			return fmt.Errorf("marking instance deletion intent: %w", err)
		}
	}
	m.lbMu.Lock()
	defer m.lbMu.Unlock()

	// Step 1: Fully deregister from load balancers. A draining or unknown
	// target is a retryable deletion barrier, never permission to delete the VM.
	if err := m.deregisterInstanceFromLB(ctx, instance); err != nil {
		return fmt.Errorf("failed to fully deregister instance from load balancers: %w", err)
	}

	// Step 2: Delete via provider (drain happens before this in reconciler)
	if err := m.provider.DeleteInstance(ctx, instanceID, *instance.ProviderID); err != nil {
		if errors.Is(err, provider.ErrInstanceNotFound) {
			m.logger.Info("Instance already deleted in provider, continuing cleanup",
				"instance_id", instanceID,
				"provider_id", *instance.ProviderID)
		} else {
			m.logger.Error("Failed to delete instance via provider",
				"instance_id", instanceID,
				"provider_id", *instance.ProviderID,
				"error", err)
			return fmt.Errorf("failed to delete instance: %w", err)
		}
	}

	// Step 3: Clean up load balancer registrations
	if err := m.localDB.DeleteLBInstancesForInstance(instanceID); err != nil {
		m.logger.Warn("Failed to delete LB instance records",
			"instance_id", instanceID,
			"error", err)
		// Continue - this is ephemeral data
	}

	// Step 4: Update instance record status
	instanceRecord, err := m.getInstanceRecord(ctx, instance.Tenant, instanceID)
	if err != nil {
		m.logger.Warn("Failed to get instance record for deletion update",
			"instance_id", instanceID,
			"error", err)
		// Continue - provider deletion was successful
	} else {
		instanceRecord.Status = infra.StatusDeleting
		instanceRecord.LastUpdated = time.Now().UTC()
		if err := m.storeInstanceRecord(ctx, instanceRecord); err != nil {
			m.logger.Warn("Failed to update instance record after deletion",
				"instance_id", instanceID,
				"error", err)
		}
	}

	// Step 5: Mark instance as deleted in local DB
	if err := m.localDB.DeleteInstance(instanceID); err != nil {
		m.logger.Warn("Failed to mark instance as deleted in local DB",
			"instance_id", instanceID,
			"error", err)
		// Continue - provider deletion was successful
	}

	m.logger.Info("Instance deletion initiated", "instance_id", instanceID)
	return nil
}

// GetInstanceStatus returns provider status for a tenant-owned instance.
func (m *Manager) GetInstanceStatus(ctx context.Context, tenant, instanceID string) (*InstanceStatus, error) {
	instance, err := m.localDB.GetInstance(instanceID)
	if err != nil {
		return nil, fmt.Errorf("instance not found in local DB: %w", err)
	}
	if instance.Tenant != tenant {
		return nil, ErrInstanceTenantMismatch
	}

	if instance.ProviderID == nil || *instance.ProviderID == "" {
		return nil, fmt.Errorf("instance has no provider ID yet")
	}

	// Get status from provider using provider instance ID
	providerStatus, err := m.provider.GetInstanceStatus(ctx, instanceID, *instance.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance status from provider: %w", err)
	}

	// Merge provider status with stored record
	return &InstanceStatus{
		InstanceID:         instanceID,
		Group:              instance.Group,
		ProviderInstanceID: providerStatus.ProviderInstanceID,
		Status:             providerStatus.Status,
		InstanceType:       providerStatus.InstanceType,
		PrivateIPv4:        providerStatus.PrivateIPv4,
		PrivateIPv6:        providerStatus.PrivateIPv6,
		CreatedAt:          instance.CreatedAt,
		LastUpdated:        time.Now().UTC(),
		Tags:               providerStatus.Tags,
	}, nil
}

// ValidateInstanceTenant verifies that an active instance belongs to tenant.
func (m *Manager) ValidateInstanceTenant(tenant, instanceID string) error {
	instance, err := m.localDB.GetInstance(instanceID)
	if err != nil {
		return fmt.Errorf("instance not found in local DB: %w", err)
	}
	if instance.Tenant != tenant {
		return ErrInstanceTenantMismatch
	}
	return nil
}

// Helper functions

// generateRegistrationNonce generates a registration nonce JWT for instance registration
func (m *Manager) generateRegistrationNonce(ctx context.Context, instanceID, kind, groupKey, tenant, clusterID, shard string, onDemand bool, expiry time.Duration) (string, error) {
	if tenant == "" {
		return "", fmt.Errorf("tenant is required")
	}

	// Get group runtime config hash
	runtimeHash := ""
	if groupKey != "" {
		group, err := m.configLoader.GetCachedGroup(tenant, groupKey)
		if err != nil {
			m.logger.Warn("Failed to get group for JWT", "group", groupKey, "error", err)
		} else if group != nil && group.RuntimeConfigHash != nil {
			runtimeHash = *group.RuntimeConfigHash
		}
	}

	return m.jwtSigner.GenerateRegistrationNonce(RegistrationNonceParams{
		SubjectID:  instanceID,
		Kind:       kind,
		ConfigHash: runtimeHash,
		ClusterID:  clusterID,
		Shard:      shard,
		Group:      groupKey,
		OnDemand:   onDemand,
		Tenant:     tenant,
		Expiry:     expiry,
	})
}

// storeInstanceRecord stores an instance record in storage
// S3 path format: instance/{shard}/{tenant}.{storage-key}.json
func (m *Manager) storeInstanceRecord(ctx context.Context, record *InstanceRecord) error {
	if record.Tenant == "" {
		return fmt.Errorf("tenant is required")
	}

	recordData, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal instance record: %w", err)
	}

	storageKey, err := StorageKey(record.InstanceID)
	if err != nil {
		return fmt.Errorf("failed to generate storage key: %w", err)
	}

	key := fmt.Sprintf("instance/%s.%s.json", record.Tenant, storageKey)
	return m.storage.Put(ctx, key, recordData)
}

// getInstanceRecord retrieves an instance record from storage
// S3 path format: instance/{tenant}.{storage-key}.json (within shard scope)
func (m *Manager) getInstanceRecord(ctx context.Context, tenant, instanceID string) (*InstanceRecord, error) {
	if tenant == "" {
		return nil, fmt.Errorf("tenant is required")
	}

	storageKey, err := StorageKey(instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate storage key: %w", err)
	}

	key := fmt.Sprintf("instance/%s.%s.json", tenant, storageKey)
	recordData, _, err := m.storage.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance record: %w", err)
	}

	var record InstanceRecord
	if err := json.Unmarshal(recordData, &record); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instance record: %w", err)
	}

	return &record, nil
}

// UpdateRegistration updates registration data for an instance using read-modify-write.
// providerID is the provider's instance ID from SQLite (may be nil if provider call failed).
// privateIPv4, privateIPv6 and hostname are authoritative values reported by the agent.
func (m *Manager) UpdateRegistration(ctx context.Context, tenant, instanceID, publicKeyPEM, certSerial string, expiresAt time.Time, providerID *string, privateIPv4, privateIPv6, hostname string) error {
	record, err := m.getInstanceRecord(ctx, tenant, instanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance record: %w", err)
	}

	now := time.Now().UTC()
	record.RegisteredAt = &now
	record.PublicKeyPEM = publicKeyPEM
	record.CertSerial = certSerial
	record.CertExpiresAt = &expiresAt
	record.LastUpdated = now

	if providerID != nil {
		record.ProviderInstanceID = *providerID
	}
	if privateIPv4 != "" {
		record.PrivateIPv4 = privateIPv4
	}
	if privateIPv6 != "" {
		record.PrivateIPv6 = privateIPv6
	}
	if hostname != "" {
		record.Hostname = hostname
	}

	return m.storeInstanceRecord(ctx, record)
}

// getShardID returns the current shard ID for storage organization
func (m *Manager) getShardID() string {
	config := m.configLoader.GetCurrent()
	if config != nil {
		return config.Shard.ID
	}
	return "unknown"
}

// RebuildCache rebuilds the local SQLite cache from S3 and provider on leadership acquisition
func (m *Manager) RebuildCache(ctx context.Context) error {
	m.logger.Info("Rebuilding local cache from S3 and provider")

	shard := m.getShardID()
	if shard == "unknown" {
		return fmt.Errorf("cannot rebuild cache: shard ID unknown")
	}

	// Step 1: Load S3-restorable data (critical instance records)
	if err := m.seedFromS3(ctx, shard); err != nil {
		m.logger.Error("Failed to seed cache from S3", "error", err)
		return fmt.Errorf("failed to seed from S3: %w", err)
	}

	// Step 2: Load provider state
	// Note: EC2 instances that were terminated while the server was down will be marked as deleted
	if err := m.seedFromProvider(ctx, shard); err != nil {
		m.logger.Error("Failed to seed cache from provider", "error", err)
		return fmt.Errorf("failed to seed from provider: %w", err)
	}

	m.logger.Info("Cache rebuild complete")
	return nil
}

// ReconcileLoadBalancers registers healthy instances without racing deletion.
func (m *Manager) ReconcileLoadBalancers(ctx context.Context, instanceID string) error {
	m.lbMu.Lock()
	defer m.lbMu.Unlock()

	instance, err := m.localDB.GetInstance(instanceID)
	if err != nil {
		return fmt.Errorf("getting instance: %w", err)
	}
	if instance == nil {
		return fmt.Errorf("instance not found: %s", instanceID)
	}
	if instance.DrainStartedAt != nil {
		return nil
	}

	cfg := m.configLoader.GetCurrent()
	if cfg == nil {
		return fmt.Errorf("configuration not loaded")
	}
	if len(cfg.LoadBalancers) == 0 {
		return nil
	}
	group, err := config.GetGroup(ctx, m.configLoader, instance.Tenant, instance.Group)
	if err != nil {
		m.logger.Debug("No group configuration for instance",
			"instance_id", instanceID,
			"tenant", instance.Tenant,
			"group", instance.Group)
		return nil
	}

	for _, lbKey := range group.LoadBalancers {
		if err := m.localDB.UpsertLBInstance(lbKey, instance.ID, localdb.LBStatusPending); err != nil {
			m.logger.Error("Failed to create pending LB registration", "instance_id", instance.ID, "lb_key", lbKey, "error", err)
			continue
		}
		if err := m.reconcileLoadBalancerTarget(ctx, cfg, instance, lbKey); err != nil {
			m.logger.Error("Failed to register instance with load balancer", "instance_id", instance.ID, "lb_key", lbKey, "error", err)
		}
	}

	pending, err := m.localDB.GetPendingOrFailedLBInstances()
	if err != nil {
		return fmt.Errorf("getting pending load balancer registrations: %w", err)
	}
	for _, target := range pending {
		instance, err := m.localDB.GetInstance(target.InstanceID)
		if err != nil || instance == nil || instance.ProviderID == nil || instance.DrainStartedAt != nil {
			continue
		}
		if err := m.reconcileLoadBalancerTarget(ctx, cfg, instance, target.LBKey); err != nil {
			m.logger.Error("Failed to retry load balancer registration", "instance_id", instance.ID, "lb_key", target.LBKey, "error", err)
		}
	}

	return nil
}

// reconcileLoadBalancerTarget advances one provider target towards healthy registration.
func (m *Manager) reconcileLoadBalancerTarget(ctx context.Context, cfg *config.Config, instance *localdb.Instance, lbKey string) error {
	if instance.ProviderID == nil {
		return fmt.Errorf("instance has no provider ID")
	}
	lb, ok := cfg.LoadBalancers[lbKey]
	if !ok {
		return fmt.Errorf("load balancer %s is not configured", lbKey)
	}
	if lb.Provider == "tunnel" {
		return nil
	}

	req := infra.RegisterLBRequest{
		ProviderInstanceID: *instance.ProviderID,
		LBConfig:           infra.LoadBalancerConfigForProvider(lb),
		Zone:               cfg.Shard.Infra.Zone,
	}
	state, err := m.provider.GetLBTargetState(ctx, req)
	if err != nil {
		return fmt.Errorf("checking target state: %w", err)
	}
	if state != infra.LBTargetDeregistered && state != infra.LBTargetPartial && state != infra.LBTargetHealthy {
		return nil
	}
	if state != infra.LBTargetHealthy {
		if err := m.provider.RegisterWithLB(ctx, req); err != nil {
			if updateErr := m.localDB.UpsertLBInstance(lbKey, instance.ID, localdb.LBStatusFailed); updateErr != nil {
				m.logger.Error("Failed to record load balancer registration failure", "instance_id", instance.ID, "lb_key", lbKey, "error", updateErr)
			}
			return fmt.Errorf("registering target: %w", err)
		}
		state, err = m.provider.GetLBTargetState(ctx, req)
		if err != nil {
			return fmt.Errorf("confirming target state: %w", err)
		}
	}
	if state != infra.LBTargetHealthy {
		return nil
	}
	if err := m.localDB.UpsertLBInstance(lbKey, instance.ID, localdb.LBStatusRegistered); err != nil {
		return fmt.Errorf("recording target registration: %w", err)
	}
	m.logger.Info("Successfully registered instance with load balancer",
		"instance_id", instance.ID,
		"provider_instance_id", *instance.ProviderID,
		"lb_key", lbKey)
	return nil
}

// deregisterInstanceFromLB removes an instance from all its registered load balancers
func (m *Manager) deregisterInstanceFromLB(ctx context.Context, instance *localdb.Instance) error {
	if instance.ProviderID == nil {
		return fmt.Errorf("instance has no provider ID")
	}

	lbInstances, err := m.localDB.GetLBInstancesForInstance(instance.ID)
	if err != nil {
		return fmt.Errorf("getting LB instances: %w", err)
	}
	if len(lbInstances) == 0 {
		return nil
	}

	cfg := m.configLoader.GetCurrent()
	if cfg == nil {
		return fmt.Errorf("configuration not loaded")
	}

	m.logger.Info("Deregistering instance from load balancers",
		"instance_id", instance.ID,
		"provider_instance_id", *instance.ProviderID,
		"lb_count", len(lbInstances))

	for _, lbInstance := range lbInstances {
		lbConfig, exists := cfg.LoadBalancers[lbInstance.LBKey]
		if !exists {
			return fmt.Errorf("load balancer %s is no longer configured; cannot safely deregister instance %s", lbInstance.LBKey, instance.ID)
		}
		if lbConfig.Provider == "tunnel" {
			continue
		}

		stateReq := infra.RegisterLBRequest{
			ProviderInstanceID: *instance.ProviderID,
			LBConfig:           infra.LoadBalancerConfigForProvider(lbConfig),
			Zone:               cfg.Shard.Infra.Zone,
		}
		state, err := m.provider.GetLBTargetState(ctx, stateReq)
		if err != nil {
			return fmt.Errorf("checking instance %s load balancer %s target state: %w", instance.ID, lbInstance.LBKey, err)
		}
		if state != infra.LBTargetDeregistered {
			if err := m.provider.DeregisterFromLB(ctx, infra.DeregisterLBRequest(stateReq)); err != nil {
				if updateErr := m.localDB.UpsertLBInstance(lbInstance.LBKey, instance.ID, localdb.LBStatusFailed); updateErr != nil {
					m.logger.Error("Failed to update LB deregistration status to failed", "error", updateErr)
				}
				return fmt.Errorf("deregistering instance %s from load balancer %s: %w", instance.ID, lbInstance.LBKey, err)
			}
			state, err = m.provider.GetLBTargetState(ctx, stateReq)
			if err != nil {
				return fmt.Errorf("confirming instance %s deregistration from load balancer %s: %w", instance.ID, lbInstance.LBKey, err)
			}
		}
		if state != infra.LBTargetDeregistered {
			return fmt.Errorf("instance %s target is still %s in load balancer %s", instance.ID, state, lbInstance.LBKey)
		}

		if err := m.localDB.UpsertLBInstance(lbInstance.LBKey, instance.ID, localdb.LBStatusDeregistered); err != nil {
			return fmt.Errorf("recording instance %s deregistration from load balancer %s: %w", instance.ID, lbInstance.LBKey, err)
		}
		m.logger.Info("Successfully deregistered instance from load balancer",
			"instance_id", instance.ID,
			"provider_instance_id", *instance.ProviderID,
			"lb_key", lbInstance.LBKey)
	}

	return nil
}

// seedFromS3 loads instance records from S3
func (m *Manager) seedFromS3(ctx context.Context, shard string) error {
	m.logger.Info("Loading instance records from S3", "shard", shard)

	// List all instance files (within shard scope)
	prefix := "instance/"
	keys, err := m.storage.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("failed to list S3 instance files: %w", err)
	}

	m.logger.Info("Found instance records in S3", "count", len(keys))

	// Load each instance record
	var instances []*localdb.Instance
	for _, key := range keys {
		recordData, _, err := m.storage.Get(ctx, key)
		if err != nil {
			m.logger.Warn("Failed to load instance record from S3", "key", key, "error", err)
			continue
		}

		var record InstanceRecord
		if err := json.Unmarshal(recordData, &record); err != nil {
			m.logger.Warn("Failed to unmarshal instance record", "key", key, "error", err)
			continue
		}

		// Convert InstanceRecord to localdb.Instance
		var providerIDPtr *string
		if record.ProviderInstanceID != "" {
			pid := record.ProviderInstanceID
			providerIDPtr = &pid
		}
		infraHash := record.InfraConfigHash
		instance := &localdb.Instance{
			ID:         record.InstanceID,
			Tenant:     record.Tenant,
			Group:      record.Group,
			OnDemand:   record.OnDemand,
			ProviderID: providerIDPtr,
			// Note: Hostname, IP4, IP6, ProviderAt will be filled by seedFromProvider
			// Registration data
			Nonce:           record.RegistrationJWT,
			IssuedAt:        &record.CreatedAt,
			RegisteredAt:    record.RegisteredAt,
			InfraConfigHash: &infraHash,
			// Time-based ephemeral data (Health, HealthAt, DrainStartedAt, DrainAckedAt) intentionally left nil
			// These will be populated by next health report or reconciliation event
			CreatedAt: record.CreatedAt,
			UpdatedAt: &record.LastUpdated,
		}

		instances = append(instances, instance)
	}

	m.logger.Info("Loaded instances from S3", "count", len(instances))

	// Seed database with S3 data
	if len(instances) > 0 {
		if err := m.localDB.SeedFromS3Data(instances); err != nil {
			return fmt.Errorf("failed to seed database from S3: %w", err)
		}
	}

	return nil
}

// seedFromProvider loads instance state from provider API and marks instances
// as deleted if they no longer exist in the provider.
func (m *Manager) seedFromProvider(ctx context.Context, shard string) error {
	m.logger.Info("Loading instance state from provider", "shard", shard)

	currentConfig := m.configLoader.GetCurrent()
	if currentConfig == nil {
		return fmt.Errorf("no configuration available")
	}

	// Fetch all instances using pagination
	var allInstances []*infra.InstanceStatus
	var nextToken string
	const pageSize = 100

	for {
		req := infra.ListInstancesRequest{
			ClusterID: currentConfig.Cluster.ID,
			Shard:     shard,
			Limit:     pageSize,
			NextToken: nextToken,
		}

		resp, err := m.provider.ListInstances(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to list provider instances: %w", err)
		}

		allInstances = append(allInstances, resp.Instances...)

		m.logger.Debug("Fetched provider instances page",
			"count", len(resp.Instances),
			"total_so_far", len(allInstances),
			"has_next", resp.NextToken != "")

		if resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}

	m.logger.Info("Found instances in provider", "count", len(allInstances))

	// Build ownership map: provider_id -> instance_id
	// Provider IDs may be reused (e.g. Proxmox VMIDs), so we track which
	// instance currently owns each provider ID.
	providerOwnership := make(map[string]string)
	var instancesToUpdate []*localdb.Instance

	for _, pi := range allInstances {
		if pi.ProviderInstanceID == "" {
			m.logger.Warn("Provider instance has empty ProviderInstanceID, skipping")
			continue
		}

		providerOwnership[pi.ProviderInstanceID] = pi.InstanceID

		if pi.InstanceID == "" {
			m.logger.Warn("Provider instance missing InstanceID", "provider_id", pi.ProviderInstanceID)
			continue
		}

		now := time.Now().UTC()
		var hostname *string
		if pi.Hostname != "" {
			hostname = &pi.Hostname
		}
		var ip4, ip6 *string
		if pi.PrivateIPv4 != "" {
			ip4 = &pi.PrivateIPv4
		}
		if pi.PrivateIPv6 != "" {
			ip6 = &pi.PrivateIPv6
		}
		instance := &localdb.Instance{
			ID:         pi.InstanceID,
			ProviderID: &pi.ProviderInstanceID,
			IP4:        ip4,
			IP6:        ip6,
			Hostname:   hostname,
			ProviderAt: &now,
		}
		stateJSON, err := json.Marshal(pi)
		if err != nil {
			m.logger.Warn("Failed to marshal provider state", "instance_id", pi.InstanceID, "error", err)
		} else {
			instance.ProviderState = stateJSON
		}
		instancesToUpdate = append(instancesToUpdate, instance)
	}

	m.logger.Info("Loaded instances from provider", "count", len(instancesToUpdate))

	// Update instances that exist in provider with current IP/hostname
	if len(instancesToUpdate) > 0 {
		if err := m.localDB.SeedFromProviderData(instancesToUpdate); err != nil {
			return fmt.Errorf("failed to seed database from provider: %w", err)
		}
	}

	// Mark instances as deleted if their provider_id is missing from the provider
	// or now belongs to a different instance (e.g. proxmox VMID reuse after deletion)
	markedDeleted, err := m.localDB.MarkInstancesDeletedIfProviderMissing(providerOwnership)
	if err != nil {
		return fmt.Errorf("failed to mark deleted instances: %w", err)
	}
	if len(markedDeleted) > 0 {
		m.logger.Info("Marked instances as deleted (provider missing)", "count", len(markedDeleted), "instance_ids", markedDeleted)
	}

	return nil
}

// selectSubnetWithCapacity selects a provider subnet ID with available capacity.
// Resolves the subnet pool to provider IDs and returns the first with capacity.
func (m *Manager) selectSubnetWithCapacity(ctx context.Context, cfg *config.Config, subnetPool string) (subnetID, key string, err error) {
	subnetIDs, resolveErr := cfg.ResolveSubnetKey(subnetPool)
	if resolveErr != nil {
		return "", "", fmt.Errorf("failed to resolve subnet pool %q: %w", subnetPool, resolveErr)
	}
	if len(subnetIDs) == 0 {
		return "", "", fmt.Errorf("subnet pool %q has no provider subnet IDs", subnetPool)
	}

	for _, id := range subnetIDs {
		hasCapacity, capacityErr := m.provider.CheckSubnetCapacity(ctx, id)
		if capacityErr != nil {
			m.logger.Warn("Failed to check subnet capacity", "subnet_pool", subnetPool, "subnet_id", id, "error", capacityErr)
			continue
		}
		if hasCapacity {
			return id, subnetPool, nil
		}
		m.logger.Debug("Subnet has no capacity", "subnet_pool", subnetPool, "subnet_id", id)
	}

	return "", "", fmt.Errorf("no subnets with available capacity for subnet pool %q", subnetPool)
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package mock

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// Provider implements both the Provider and LoadBalancerProvider interfaces for testing
type Provider struct {
	config    provider.ProviderConfig
	logger    *slog.Logger
	instances map[string]*provider.InstanceStatus
	mu        sync.RWMutex

	// Testing controls
	createDelay time.Duration
	createError error
	deleteError error
	statusError error

	// Load balancer state
	lbInstances map[string]map[string]bool // groupKey -> providerInstanceID -> registered
}

// Options contains options for creating a mock provider
type Options struct {
	Config provider.ProviderConfig
	Logger *slog.Logger
}

// NewProvider creates a new mock provider
func NewProvider(opts Options) *Provider {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Provider{
		config:      opts.Config,
		logger:      opts.Logger,
		instances:   make(map[string]*provider.InstanceStatus),
		lbInstances: make(map[string]map[string]bool),
	}
}

func (p *Provider) Kind() string {
	return "mock"
}

// CreateInstance simulates creating an instance
func (p *Provider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.CreateInstanceResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check for testing error injection
	if p.createError != nil {
		return nil, p.createError
	}

	// Simulate creation delay
	if p.createDelay > 0 {
		time.Sleep(p.createDelay)
	}

	p.logger.Info("Creating mock instance",
		"instance_id", req.InstanceID,
		"instance_type", req.InstanceType)

	// Check if instance already exists
	if _, exists := p.instances[req.InstanceID]; exists {
		return nil, fmt.Errorf("instance already exists: %s", req.InstanceID)
	}

	// Generate mock provider instance ID
	providerInstanceID := fmt.Sprintf("i-%s", req.InstanceID[len(req.InstanceID)-12:])

	// Build tags
	tags := make(map[string]string)
	tags[tagInstanceID] = req.InstanceID
	tags[tagNstanceManaged] = "true"
	if req.ClusterID != "" {
		tags[tagClusterID] = req.ClusterID
	}
	if req.Shard != "" {
		tags[tagShard] = req.Shard
	}
	if req.Group != "" {
		tags[tagGroup] = req.Group
	}
	if req.Template != "" {
		tags[tagTemplate] = req.Template
	}
	if req.InstanceKind != "" {
		tags[tagInstanceKind] = req.InstanceKind
	}
	for key, value := range req.CustomTags {
		tags[key] = value
	}

	// Create instance status
	status := &provider.InstanceStatus{
		InstanceID:         req.InstanceID,
		ProviderInstanceID: providerInstanceID,
		Status:             provider.StatusPending,
		InstanceType:       req.InstanceType,
		PrivateIPv4:        "10.0.1." + req.InstanceID[len(req.InstanceID)-3:],  // Mock IPv4
		PrivateIPv6:        "fd00::1:" + req.InstanceID[len(req.InstanceID)-3:], // Mock IPv6
		Hostname:           req.InstanceID,
		LaunchedAt:         time.Now().UTC(),
		Tags:               tags,
		Region:             p.config.Region,
		Zone:               p.config.Zone,
		// "Association" Metadata
		ClusterID: req.ClusterID,
		Shard:     req.Shard,
	}

	// Populate "Annotation" Metadata if available
	if req.Group != "" || req.InstanceKind != "" {
		status.Annotations = &provider.InstanceAnnotations{
			Group: req.Group,
			Kind:  req.InstanceKind,
		}
	}

	// Store instance
	p.instances[req.InstanceID] = status

	// Simulate transition to running after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		p.mu.Lock()
		defer p.mu.Unlock()
		if instance, exists := p.instances[req.InstanceID]; exists && instance.Status == provider.StatusPending {
			instance.Status = provider.StatusRunning
		}
	}()

	return &provider.CreateInstanceResponse{
		InstanceID:         req.InstanceID,
		ProviderInstanceID: providerInstanceID,
		Status:             provider.StatusPending,
		PrivateIPv4:        status.PrivateIPv4,
		PrivateIPv6:        status.PrivateIPv6,
		Hostname:           status.Hostname,
		LaunchedAt:         status.LaunchedAt,
		Tags:               status.Tags,
	}, nil
}

// DeleteInstance simulates deleting an instance by provider instance ID
func (p *Provider) DeleteInstance(ctx context.Context, instanceID, providerInstanceID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check for testing error injection
	if p.deleteError != nil {
		return p.deleteError
	}

	p.logger.Info("Deleting mock instance", "provider_instance_id", providerInstanceID)

	// Find instance by provider instance ID
	var foundInstanceID string
	for instanceID, instance := range p.instances {
		if instance.ProviderInstanceID == providerInstanceID {
			foundInstanceID = instanceID
			break
		}
	}
	if foundInstanceID == "" {
		return fmt.Errorf("%w: %s", provider.ErrInstanceNotFound, providerInstanceID)
	}

	// Transition to deleting, then deleted
	instance := p.instances[foundInstanceID]
	instance.Status = provider.StatusDeleting

	// Simulate deletion after delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		p.mu.Lock()
		defer p.mu.Unlock()
		if instance, exists := p.instances[foundInstanceID]; exists {
			instance.Status = provider.StatusDeleted
		}
	}()

	return nil
}

// GetInstanceStatus returns the status of a mock instance
func (p *Provider) GetInstanceStatus(ctx context.Context, instanceID, providerInstanceID string) (*provider.InstanceStatus, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Check for testing error injection
	if p.statusError != nil {
		return nil, p.statusError
	}

	// Find instance by provider instance ID
	for _, instance := range p.instances {
		if instance.ProviderInstanceID == providerInstanceID {
			// Return a copy to avoid data races
			status := *instance
			return &status, nil
		}
	}

	return nil, fmt.Errorf("%w: %s", provider.ErrInstanceNotFound, providerInstanceID)
}

// ListInstances returns mock instances with pagination support
func (p *Provider) ListInstances(ctx context.Context, req provider.ListInstancesRequest) (*provider.ListInstancesResponse, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	p.logger.Debug("Listing mock instances",
		"cluster_id", req.ClusterID,
		"shard", req.Shard,
		"limit", req.Limit)

	// Set default limit
	if req.Limit <= 0 {
		req.Limit = 100
	}

	var allResults []*provider.InstanceStatus
	for _, instance := range p.instances {
		// Apply cluster/zone filters
		matches := true
		if req.ClusterID != "" {
			if clusterID, exists := instance.Tags[tagClusterID]; !exists || clusterID != req.ClusterID {
				matches = false
			}
		}
		if req.Shard != "" {
			if shard, exists := instance.Tags[tagShard]; !exists || shard != req.Shard {
				matches = false
			}
		}

		if matches {
			// Return a copy
			status := *instance
			allResults = append(allResults, &status)
		}
	}

	// Handle pagination
	startIdx := 0
	if req.NextToken != "" {
		if _, err := fmt.Sscanf(req.NextToken, "%d", &startIdx); err != nil {
			return nil, fmt.Errorf("invalid pagination token: %w", err)
		}
	}

	endIdx := startIdx + req.Limit
	if endIdx > len(allResults) {
		endIdx = len(allResults)
	}

	var results []*provider.InstanceStatus
	if startIdx < len(allResults) {
		results = allResults[startIdx:endIdx]
	}

	response := &provider.ListInstancesResponse{
		Instances: results,
	}

	// Set next token if there are more results
	if endIdx < len(allResults) {
		response.NextToken = fmt.Sprintf("%d", endIdx)
	}

	return response, nil
}

// AssignLeaderNetwork is a no-op for mock provider
func (p *Provider) AssignLeaderNetwork(ctx context.Context, providerInstanceID string, ln provider.LeaderNetwork) error {
	p.logger.Info("Mock leader network assign operation", "instance_id", providerInstanceID, "ip", ln.IP)
	return nil
}

// ReleaseLeaderNetwork is a no-op for mock provider
func (p *Provider) ReleaseLeaderNetwork(ctx context.Context, providerInstanceID string, ln provider.LeaderNetwork) error {
	p.logger.Info("Mock leader network release operation", "instance_id", providerInstanceID, "ip", ln.IP)
	return nil
}

// CheckSubnetCapacity simulates checking subnet capacity (always returns true for mock)
func (p *Provider) CheckSubnetCapacity(ctx context.Context, subnetID string) (bool, error) {
	p.logger.Debug("Mock: Checking subnet capacity", "subnet_id", subnetID)
	return true, nil
}

// RegisterWithLB registers an instance with a mock load balancer
func (p *Provider) RegisterWithLB(ctx context.Context, req provider.RegisterLBRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	lbKey := p.getLBKey(req.LBConfig)
	if p.lbInstances[lbKey] == nil {
		p.lbInstances[lbKey] = make(map[string]bool)
	}

	p.lbInstances[lbKey][req.ProviderInstanceID] = true
	p.logger.Info("Mock: Registered instance with load balancer",
		"provider_instance_id", req.ProviderInstanceID,
		"lb_key", lbKey)

	return nil
}

// DeregisterFromLB removes an instance from a mock load balancer
func (p *Provider) DeregisterFromLB(ctx context.Context, req provider.DeregisterLBRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	lbKey := p.getLBKey(req.LBConfig)
	if p.lbInstances[lbKey] != nil {
		delete(p.lbInstances[lbKey], req.ProviderInstanceID)
	}

	p.logger.Info("Mock: Deregistered instance from load balancer",
		"provider_instance_id", req.ProviderInstanceID,
		"lb_key", lbKey)

	return nil
}

// GetLBTargetState reports whether a target is present in the mock load balancer.
func (p *Provider) GetLBTargetState(ctx context.Context, req provider.RegisterLBRequest) (provider.LBTargetState, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.lbInstances[p.getLBKey(req.LBConfig)][req.ProviderInstanceID] {
		return provider.LBTargetHealthy, nil
	}
	return provider.LBTargetDeregistered, nil
}

// ListLBInstances lists all instances currently registered with a mock load balancer
func (p *Provider) ListLBInstances(ctx context.Context, req provider.ListLBInstancesRequest) ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	lbKey := p.getLBKey(req.LBConfig)
	var instanceIDs []string

	if instances, ok := p.lbInstances[lbKey]; ok {
		for instanceID := range instances {
			instanceIDs = append(instanceIDs, instanceID)
		}
	}

	p.logger.Debug("Mock: Listed instances in load balancer",
		"lb_key", lbKey,
		"count", len(instanceIDs))

	return instanceIDs, nil
}

func (p *Provider) getLBKey(cfg provider.LoadBalancerConfig) string {
	if len(cfg.TargetGroups) > 0 {
		return cfg.TargetGroups[0].ARN
	}
	if len(cfg.NetworkEndpointGroups) > 0 {
		return cfg.NetworkEndpointGroups[0]
	}
	return "default"
}

// Testing helpers

// SetCreateDelay sets a delay for instance creation (for testing)
func (p *Provider) SetCreateDelay(delay time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.createDelay = delay
}

// SetCreateError sets an error to return on instance creation (for testing)
func (p *Provider) SetCreateError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.createError = err
}

// SetDeleteError sets an error to return on instance deletion (for testing)
func (p *Provider) SetDeleteError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleteError = err
}

// SetStatusError sets an error to return on status queries (for testing)
func (p *Provider) SetStatusError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statusError = err
}

// Reset clears all instances and resets testing controls
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.instances = make(map[string]*provider.InstanceStatus)
	p.lbInstances = make(map[string]map[string]bool)
	p.createDelay = 0
	p.createError = nil
	p.deleteError = nil
	p.statusError = nil
}

// GetInstanceCount returns the number of instances (for testing)
func (p *Provider) GetInstanceCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.instances)
}

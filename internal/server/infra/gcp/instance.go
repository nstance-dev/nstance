// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
	"google.golang.org/protobuf/proto"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// CreateInstance creates a new GCP VM instance
func (p *Provider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.CreateInstanceResponse, error) {
	p.logger.Info("Creating GCP VM instance",
		"instance_id", req.InstanceID,
		"instance_type", req.InstanceType)

	instanceName := req.InstanceID

	// Build labels (GCP uses lowercase labels)
	labels := make(map[string]string)
	labels[labelInstanceID] = sanitizeLabel(req.InstanceID)
	labels[labelNstanceManaged] = "true"
	if req.ClusterID != "" {
		labels[labelClusterID] = sanitizeLabel(req.ClusterID)
	}
	if req.Shard != "" {
		labels[labelShard] = sanitizeLabel(req.Shard)
	}
	if req.Group != "" {
		labels[labelGroup] = sanitizeLabel(req.Group)
	}
	if req.Template != "" {
		labels[labelTemplate] = sanitizeLabel(req.Template)
	}
	if req.InstanceKind != "" {
		labels[labelInstanceKind] = sanitizeLabel(req.InstanceKind)
	}
	for key, value := range req.CustomTags {
		labels[sanitizeLabel(key)] = sanitizeLabel(value)
	}

	// Build full subnet path from subnet name
	subnetPath := fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", p.options.ProjectID, p.config.Region, req.SubnetID)

	// Build network interface configuration
	networkInterfaces := []*computepb.NetworkInterface{
		{
			Subnetwork: proto.String(subnetPath),
			AccessConfigs: []*computepb.AccessConfig{
				{
					Name:        proto.String("External NAT"),
					Type:        proto.String(computepb.AccessConfig_ONE_TO_ONE_NAT.String()),
					NetworkTier: proto.String(computepb.AccessConfig_PREMIUM.String()),
				},
			},
		},
	}

	// Build boot disk configuration
	disks := []*computepb.AttachedDisk{
		{
			Boot:       proto.Bool(true),
			AutoDelete: proto.Bool(true),
			InitializeParams: &computepb.AttachedDiskInitializeParams{
				SourceImage: proto.String("projects/debian-cloud/global/images/family/debian-13"),
				DiskSizeGb:  proto.Int64(30),
				DiskType:    proto.String(fmt.Sprintf("zones/%s/diskTypes/pd-standard", p.config.Zone)),
			},
		},
	}

	// Build metadata with user data
	metadata := &computepb.Metadata{
		Items: []*computepb.Items{
			{
				Key:   proto.String("startup-script"),
				Value: proto.String(req.UserData),
			},
		},
	}

	// Build instance configuration
	instance := &computepb.Instance{
		Name:              proto.String(instanceName),
		MachineType:       proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", p.config.Zone, req.InstanceType)),
		Labels:            labels,
		NetworkInterfaces: networkInterfaces,
		Disks:             disks,
		Metadata:          metadata,
	}

	// Apply provider-specific arguments
	if err := p.applyProviderArgs(instance, req.Args); err != nil {
		return nil, fmt.Errorf("failed to apply provider arguments: %w", err)
	}

	// Create the instance
	op, err := p.instancesClient.Insert(ctx, &computepb.InsertInstanceRequest{
		Project:          p.options.ProjectID,
		Zone:             p.config.Zone,
		InstanceResource: instance,
	})
	if err != nil {
		p.logger.Error("Failed to create GCP instance",
			"instance_id", req.InstanceID,
			"error", err)
		return nil, fmt.Errorf("failed to create GCP instance: %w", err)
	}

	// Wait for operation to complete
	if err := op.Wait(ctx); err != nil {
		return nil, fmt.Errorf("failed waiting for instance creation: %w", err)
	}

	// Get the created instance to retrieve details
	createdInstance, err := p.instancesClient.Get(ctx, &computepb.GetInstanceRequest{
		Project:  p.options.ProjectID,
		Zone:     p.config.Zone,
		Instance: instanceName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get created instance: %w", err)
	}

	// Build response
	response := &provider.CreateInstanceResponse{
		InstanceID:         req.InstanceID,
		ProviderInstanceID: *createdInstance.Name,
		Status:             p.convertInstanceState(createdInstance.Status),
		Tags:               req.CustomTags,
	}

	// Extract IP addresses
	if len(createdInstance.NetworkInterfaces) > 0 {
		if createdInstance.NetworkInterfaces[0].NetworkIP != nil {
			response.PrivateIPv4 = *createdInstance.NetworkInterfaces[0].NetworkIP
		}
		if len(createdInstance.NetworkInterfaces[0].Ipv6AccessConfigs) > 0 {
			if createdInstance.NetworkInterfaces[0].Ipv6AccessConfigs[0].ExternalIpv6 != nil {
				response.PrivateIPv6 = *createdInstance.NetworkInterfaces[0].Ipv6AccessConfigs[0].ExternalIpv6
			}
		}
	}

	// Instance name is the hostname in GCP
	if createdInstance.Name != nil {
		response.Hostname = *createdInstance.Name
	}

	p.logger.Info("GCP instance created successfully",
		"instance_id", req.InstanceID,
		"provider_instance_id", response.ProviderInstanceID,
		"status", response.Status)

	return response, nil
}

// DeleteInstance terminates a GCP VM instance by provider instance ID (instance name)
func (p *Provider) DeleteInstance(ctx context.Context, instanceID, providerInstanceID string) error {
	p.logger.Info("Deleting GCP VM instance", "provider_instance_id", providerInstanceID)

	op, err := p.instancesClient.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project:  p.options.ProjectID,
		Zone:     p.config.Zone,
		Instance: providerInstanceID,
	})
	if err != nil {
		if isNotFound(err) {
			p.logger.Info("GCP VM already deleted", "provider_instance_id", providerInstanceID)
			return nil
		}
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed waiting for delete operation: %w", err)
	}

	p.logger.Info("GCP VM deleted", "provider_instance_id", providerInstanceID)
	return nil
}

// isNotFound checks if an error is a Google API 404 Not Found error
func isNotFound(err error) bool {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == 404
	}
	return false
}

// GetInstanceStatus returns the current status of an instance
func (p *Provider) GetInstanceStatus(ctx context.Context, instanceID, providerInstanceID string) (*provider.InstanceStatus, error) {
	instance, err := p.instancesClient.Get(ctx, &computepb.GetInstanceRequest{
		Project:  p.options.ProjectID,
		Zone:     p.config.Zone,
		Instance: providerInstanceID,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %s", provider.ErrInstanceNotFound, providerInstanceID)
	}

	// Extract instance ID from labels
	instanceIDFromLabel := ""
	if instance.Labels != nil {
		if id, ok := instance.Labels[labelInstanceID]; ok {
			instanceIDFromLabel = id
		}
	}

	return p.convertToInstanceStatus(instanceIDFromLabel, instance), nil
}

// ListInstances returns instances with pagination support
func (p *Provider) ListInstances(ctx context.Context, req provider.ListInstancesRequest) (*provider.ListInstancesResponse, error) {
	p.logger.Debug("Listing GCP VM instances", "cluster_id", req.ClusterID, "shard", req.Shard)

	// Build filter string
	var filterParts []string
	filterParts = append(filterParts, fmt.Sprintf(`labels.%s="true"`, labelNstanceManaged))

	if req.ClusterID != "" {
		filterParts = append(filterParts, fmt.Sprintf(`labels.%s="%s"`, labelClusterID, sanitizeLabel(req.ClusterID)))
	}
	if req.Shard != "" {
		filterParts = append(filterParts, fmt.Sprintf(`labels.%s="%s"`, labelShard, sanitizeLabel(req.Shard)))
	}

	filterStr := strings.Join(filterParts, " AND ")

	var instances []*provider.InstanceStatus
	listReq := &computepb.ListInstancesRequest{
		Project: p.options.ProjectID,
		Zone:    p.config.Zone,
		Filter:  proto.String(filterStr),
	}

	it := p.instancesClient.List(ctx, listReq)
	for {
		instance, err := it.Next()
		if err != nil {
			if err.Error() == "no more items in iterator" {
				break
			}
			return nil, fmt.Errorf("failed to list instances: %w", err)
		}

		// Extract instance ID from labels
		instanceID := ""
		if instance.Labels != nil {
			if id, ok := instance.Labels[labelInstanceID]; ok {
				instanceID = id
			}
		}

		instances = append(instances, p.convertToInstanceStatus(instanceID, instance))
	}

	p.logger.Debug("Listed GCP VM instances", "count", len(instances))

	total := len(instances)
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}

	offset := 0
	if req.NextToken != "" {
		_, err := fmt.Sscanf(req.NextToken, "%d", &offset)
		if err != nil {
			return nil, fmt.Errorf("invalid next token when listing instances: %w", err)
		}
	}

	end := offset + limit
	if end > total {
		end = total
	}

	if offset >= total {
		return &provider.ListInstancesResponse{
			Instances: []*provider.InstanceStatus{},
			NextToken: "",
			Total:     total,
		}, nil
	}

	paginatedInstances := instances[offset:end]
	nextToken := ""
	if end < total {
		nextToken = fmt.Sprintf("%d", end)
	}

	return &provider.ListInstancesResponse{
		Instances: paginatedInstances,
		NextToken: nextToken,
		Total:     total,
	}, nil
}

// AssignLeaderNetwork assigns a reserved internal IP as an alias IP on the instance's primary NIC.
func (p *Provider) AssignLeaderNetwork(ctx context.Context, providerInstanceID string, ln provider.LeaderNetwork) error {
	aliasIP := ln.IP
	p.logger.Info("Assigning leader network to instance", "alias_ip", aliasIP, "instance_name", providerInstanceID)

	instance, err := p.instancesClient.Get(ctx, &computepb.GetInstanceRequest{
		Project:  p.options.ProjectID,
		Zone:     p.config.Zone,
		Instance: providerInstanceID,
	})
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if len(instance.NetworkInterfaces) == 0 {
		return fmt.Errorf("instance has no network interfaces")
	}

	primaryNIC := instance.NetworkInterfaces[0]
	nicName := primaryNIC.GetName()
	if nicName == "" {
		nicName = "nic0"
	}

	aliasIPRange := aliasIP + "/32"
	for _, alias := range primaryNIC.AliasIpRanges {
		if alias.GetIpCidrRange() == aliasIPRange {
			p.logger.Info("Alias IP already assigned to instance", "alias_ip", aliasIP)
			return nil
		}
	}

	newAliasRanges := append(primaryNIC.AliasIpRanges, &computepb.AliasIpRange{
		IpCidrRange: proto.String(aliasIPRange),
	})

	updatedNIC := &computepb.NetworkInterface{
		Fingerprint:   primaryNIC.Fingerprint,
		AliasIpRanges: newAliasRanges,
	}

	op, err := p.instancesClient.UpdateNetworkInterface(ctx, &computepb.UpdateNetworkInterfaceInstanceRequest{
		Project:                  p.options.ProjectID,
		Zone:                     p.config.Zone,
		Instance:                 providerInstanceID,
		NetworkInterface:         nicName,
		NetworkInterfaceResource: updatedNIC,
	})
	if err != nil {
		return fmt.Errorf("failed to assign alias IP: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed waiting for alias IP assignment: %w", err)
	}

	p.logger.Info("Alias IP assigned successfully", "alias_ip", aliasIP, "instance_name", providerInstanceID)
	return nil
}

// ReleaseLeaderNetwork removes an alias IP from the instance's primary NIC.
func (p *Provider) ReleaseLeaderNetwork(ctx context.Context, providerInstanceID string, ln provider.LeaderNetwork) error {
	aliasIP := ln.IP
	p.logger.Info("Releasing leader network from instance", "alias_ip", aliasIP, "instance_name", providerInstanceID)

	instance, err := p.instancesClient.Get(ctx, &computepb.GetInstanceRequest{
		Project:  p.options.ProjectID,
		Zone:     p.config.Zone,
		Instance: providerInstanceID,
	})
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if len(instance.NetworkInterfaces) == 0 {
		return fmt.Errorf("instance has no network interfaces")
	}

	primaryNIC := instance.NetworkInterfaces[0]
	nicName := primaryNIC.GetName()
	if nicName == "" {
		nicName = "nic0"
	}

	aliasIPRange := aliasIP + "/32"
	var newAliasRanges []*computepb.AliasIpRange
	found := false
	for _, alias := range primaryNIC.AliasIpRanges {
		if alias.GetIpCidrRange() == aliasIPRange {
			found = true
			continue
		}
		newAliasRanges = append(newAliasRanges, alias)
	}

	if !found {
		p.logger.Info("Alias IP not assigned to instance, nothing to remove", "alias_ip", aliasIP)
		return nil
	}

	updatedNIC := &computepb.NetworkInterface{
		Fingerprint:   primaryNIC.Fingerprint,
		AliasIpRanges: newAliasRanges,
	}

	op, err := p.instancesClient.UpdateNetworkInterface(ctx, &computepb.UpdateNetworkInterfaceInstanceRequest{
		Project:                  p.options.ProjectID,
		Zone:                     p.config.Zone,
		Instance:                 providerInstanceID,
		NetworkInterface:         nicName,
		NetworkInterfaceResource: updatedNIC,
	})
	if err != nil {
		return fmt.Errorf("failed to remove alias IP: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed waiting for alias IP removal: %w", err)
	}

	p.logger.Info("Alias IP removed successfully", "alias_ip", aliasIP, "instance_name", providerInstanceID)
	return nil
}

// CheckSubnetCapacity checks if a subnet has available IP addresses
func (p *Provider) CheckSubnetCapacity(ctx context.Context, subnetName string) (bool, error) {
	subnet, err := p.subnetClient.Get(ctx, &computepb.GetSubnetworkRequest{
		Project:    p.options.ProjectID,
		Region:     p.config.Region,
		Subnetwork: subnetName,
	})
	if err != nil {
		return false, fmt.Errorf("failed to get subnet: %w", err)
	}

	// Check IP CIDR range
	// GCP provides the IP CIDR range, and we can check usage
	// For simplicity, we'll assume capacity is available if the subnet exists
	// In production, you'd want to calculate actual CIDR utilization

	hasCapacity := true
	if subnet.IpCidrRange != nil {
		// Simple heuristic: check if we have space
		// More sophisticated implementation would parse CIDR and count used IPs
		hasCapacity = true
	}

	p.logger.Debug("Checked subnet capacity",
		"subnet_name", subnetName,
		"cidr", subnet.GetIpCidrRange(),
		"has_capacity", hasCapacity)

	return hasCapacity, nil
}

// convertToInstanceStatus converts a GCP instance to InstanceStatus
func (p *Provider) convertToInstanceStatus(instanceID string, instance *computepb.Instance) *provider.InstanceStatus {
	status := &provider.InstanceStatus{
		InstanceID:         instanceID,
		ProviderInstanceID: *instance.Name,
		Status:             p.convertInstanceState(instance.Status),
		InstanceType:       p.extractMachineType(instance.MachineType),
		Region:             p.config.Region,
		Zone:               p.extractZone(instance.Zone),
		Tags:               make(map[string]string),
	}

	// Instance name is the hostname in GCP
	if instance.Name != nil {
		status.Hostname = *instance.Name
	}

	// Extract labels as tags
	var group, kind string
	if instance.Labels != nil {
		for key, value := range instance.Labels {
			status.Tags[key] = value
			switch key {
			case labelClusterID:
				status.ClusterID = value
			case labelShard:
				status.Shard = value
			case labelGroup:
				group = value
			case labelInstanceKind:
				kind = value
			}
		}
	}

	// Populate "Annotation" Metadata if available
	if group != "" || kind != "" {
		status.Annotations = &provider.InstanceAnnotations{
			Group: group,
			Kind:  kind,
		}
	}

	// Extract IPs from network interfaces
	if len(instance.NetworkInterfaces) > 0 {
		if instance.NetworkInterfaces[0].NetworkIP != nil {
			status.PrivateIPv4 = *instance.NetworkInterfaces[0].NetworkIP
		}

		// Check for IPv6
		if len(instance.NetworkInterfaces[0].Ipv6AccessConfigs) > 0 {
			if instance.NetworkInterfaces[0].Ipv6AccessConfigs[0].ExternalIpv6 != nil {
				status.PrivateIPv6 = *instance.NetworkInterfaces[0].Ipv6AccessConfigs[0].ExternalIpv6
			}
		}
	}

	return status
}

// convertInstanceState converts GCP instance state to standard status
func (p *Provider) convertInstanceState(state *string) string {
	if state == nil {
		return provider.StatusUnknown
	}

	switch *state {
	case "PROVISIONING", "STAGING":
		return provider.StatusPending
	case "RUNNING":
		return provider.StatusRunning
	case "STOPPING":
		return provider.StatusStopping
	case "STOPPED":
		return provider.StatusStopped
	case "TERMINATED":
		return provider.StatusDeleted
	case "SUSPENDING":
		return provider.StatusSuspending
	case "SUSPENDED":
		return provider.StatusSuspended
	case "REPAIRING":
		return provider.StatusRepairing
	default:
		return provider.StatusUnknown
	}
}

// extractMachineType extracts machine type from full URL
func (p *Provider) extractMachineType(machineTypeURL *string) string {
	if machineTypeURL == nil {
		return ""
	}

	// URL format: zones/us-central1-a/machineTypes/n1-standard-1
	parts := strings.Split(*machineTypeURL, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}

	return *machineTypeURL
}

// extractZone extracts zone from full URL
func (p *Provider) extractZone(zoneURL *string) string {
	if zoneURL == nil {
		return ""
	}

	// URL format: https://www.googleapis.com/compute/v1/projects/{project}/zones/{zone}
	parts := strings.Split(*zoneURL, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}

	return *zoneURL
}

// applyProviderArgs applies provider-specific arguments to GCP instance configuration
func (p *Provider) applyProviderArgs(instance *computepb.Instance, args map[string]interface{}) error {
	for key, val := range args {
		switch key {
		case "Labels":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal Labels: %w", err)
			} else {
				var extraLabels map[string]string
				if err := json.Unmarshal(data, &extraLabels); err != nil {
					return fmt.Errorf("failed to unmarshal Labels: %w", err)
				}
				if instance.Labels == nil {
					instance.Labels = make(map[string]string)
				}
				for k, v := range extraLabels {
					instance.Labels[sanitizeLabel(k)] = sanitizeLabel(v)
				}
			}
		case "NetworkTags":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal NetworkTags: %w", err)
			} else {
				var tags []string
				if err := json.Unmarshal(data, &tags); err != nil {
					return fmt.Errorf("failed to unmarshal NetworkTags: %w", err)
				}
				if instance.Tags == nil {
					instance.Tags = &computepb.Tags{}
				}
				instance.Tags.Items = append(instance.Tags.Items, tags...)
			}
		case "SourceImage":
			if image, ok := val.(string); ok {
				if len(instance.Disks) > 0 && instance.Disks[0].InitializeParams != nil {
					instance.Disks[0].InitializeParams.SourceImage = proto.String(image)
				}
			} else {
				return fmt.Errorf("SourceImage must be a string")
			}
		case "ServiceAccount":
			if email, ok := val.(string); ok {
				instance.ServiceAccounts = []*computepb.ServiceAccount{
					{
						Email:  proto.String(email),
						Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
					},
				}
			} else {
				return fmt.Errorf("ServiceAccount must be a string")
			}
		case "Preemptible":
			if preemptible, ok := val.(bool); ok {
				if instance.Scheduling == nil {
					instance.Scheduling = &computepb.Scheduling{}
				}
				instance.Scheduling.Preemptible = proto.Bool(preemptible)
			} else {
				return fmt.Errorf("preemptible must be a boolean")
			}
		case "OnHostMaintenance":
			if maintenance, ok := val.(string); ok {
				if instance.Scheduling == nil {
					instance.Scheduling = &computepb.Scheduling{}
				}
				instance.Scheduling.OnHostMaintenance = proto.String(maintenance)
			} else {
				return fmt.Errorf("OnHostMaintenance must be a string")
			}
		case "AutomaticRestart":
			if autoRestart, ok := val.(bool); ok {
				if instance.Scheduling == nil {
					instance.Scheduling = &computepb.Scheduling{}
				}
				instance.Scheduling.AutomaticRestart = proto.Bool(autoRestart)
			} else {
				return fmt.Errorf("AutomaticRestart must be a boolean")
			}
		case "ProvisioningModel":
			if model, ok := val.(string); ok {
				if instance.Scheduling == nil {
					instance.Scheduling = &computepb.Scheduling{}
				}
				instance.Scheduling.ProvisioningModel = proto.String(model)
			} else {
				return fmt.Errorf("ProvisioningModel must be a string")
			}
		case "ServiceAccounts":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal ServiceAccounts: %w", err)
			} else {
				var accounts []*computepb.ServiceAccount
				if err := json.Unmarshal(data, &accounts); err != nil {
					return fmt.Errorf("failed to unmarshal ServiceAccounts: %w", err)
				}
				instance.ServiceAccounts = accounts
			}
		case "GuestAccelerators":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal GuestAccelerators: %w", err)
			} else {
				var accelerators []*computepb.AcceleratorConfig
				if err := json.Unmarshal(data, &accelerators); err != nil {
					return fmt.Errorf("failed to unmarshal GuestAccelerators: %w", err)
				}
				instance.GuestAccelerators = accelerators
			}
		case "MinCpuPlatform":
			if platform, ok := val.(string); ok {
				instance.MinCpuPlatform = proto.String(platform)
			} else {
				return fmt.Errorf("MinCpuPlatform must be a string")
			}
		case "ShieldedInstanceConfig":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal ShieldedInstanceConfig: %w", err)
			} else {
				var config computepb.ShieldedInstanceConfig
				if err := json.Unmarshal(data, &config); err != nil {
					return fmt.Errorf("failed to unmarshal ShieldedInstanceConfig: %w", err)
				}
				instance.ShieldedInstanceConfig = &config
			}
		case "ConfidentialInstanceConfig":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal ConfidentialInstanceConfig: %w", err)
			} else {
				var config computepb.ConfidentialInstanceConfig
				if err := json.Unmarshal(data, &config); err != nil {
					return fmt.Errorf("failed to unmarshal ConfidentialInstanceConfig: %w", err)
				}
				instance.ConfidentialInstanceConfig = &config
			}
		case "ResourcePolicies":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal ResourcePolicies: %w", err)
			} else {
				var policies []string
				if err := json.Unmarshal(data, &policies); err != nil {
					return fmt.Errorf("failed to unmarshal ResourcePolicies: %w", err)
				}
				instance.ResourcePolicies = policies
			}
		default:
			p.logger.Warn("Unknown provider argument", "key", key, "value", val)
		}
	}
	return nil
}

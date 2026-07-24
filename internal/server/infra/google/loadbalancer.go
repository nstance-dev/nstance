// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"context"
	"fmt"

	computev1 "google.golang.org/api/compute/v1"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// RegisterWithLB registers an instance with a Google Cloud instance group
func (p *Provider) RegisterWithLB(ctx context.Context, req provider.RegisterLBRequest) error {
	if req.LBConfig.InstanceGroupName == nil || *req.LBConfig.InstanceGroupName == "" {
		return fmt.Errorf("instance group name is required for Google Cloud load balancer registration")
	}
	if req.Zone == "" {
		return fmt.Errorf("zone is required for Google Cloud load balancer registration")
	}

	p.logger.Info("Registering instance with Google Cloud instance group",
		"provider_instance_id", req.ProviderInstanceID,
		"instance_group", *req.LBConfig.InstanceGroupName,
		"zone", req.Zone)

	instanceURL := fmt.Sprintf("projects/%s/zones/%s/instances/%s",
		p.options.ProjectID, req.Zone, req.ProviderInstanceID)

	instancesAddRequest := &computev1.InstanceGroupsAddInstancesRequest{
		Instances: []*computev1.InstanceReference{
			{
				Instance: instanceURL,
			},
		},
	}

	_, err := p.computeService.InstanceGroups.AddInstances(
		p.options.ProjectID,
		req.Zone,
		*req.LBConfig.InstanceGroupName,
		instancesAddRequest,
	).Context(ctx).Do()

	if err != nil {
		p.logger.Error("Failed to register instance with instance group",
			"provider_instance_id", req.ProviderInstanceID,
			"instance_group", *req.LBConfig.InstanceGroupName,
			"zone", req.Zone,
			"error", err)
		return fmt.Errorf("registering instance with instance group: %w", err)
	}

	p.logger.Info("Successfully registered instance with instance group",
		"provider_instance_id", req.ProviderInstanceID,
		"instance_group", *req.LBConfig.InstanceGroupName,
		"zone", req.Zone)

	return nil
}

// DeregisterFromLB removes an instance from a Google Cloud instance group
func (p *Provider) DeregisterFromLB(ctx context.Context, req provider.DeregisterLBRequest) error {
	if req.LBConfig.InstanceGroupName == nil || *req.LBConfig.InstanceGroupName == "" {
		return fmt.Errorf("instance group name is required for Google Cloud load balancer deregistration")
	}
	if req.Zone == "" {
		return fmt.Errorf("zone is required for Google Cloud load balancer deregistration")
	}

	p.logger.Info("Deregistering instance from Google Cloud instance group",
		"provider_instance_id", req.ProviderInstanceID,
		"instance_group", *req.LBConfig.InstanceGroupName,
		"zone", req.Zone)

	instanceURL := fmt.Sprintf("projects/%s/zones/%s/instances/%s",
		p.options.ProjectID, req.Zone, req.ProviderInstanceID)

	instancesRemoveRequest := &computev1.InstanceGroupsRemoveInstancesRequest{
		Instances: []*computev1.InstanceReference{
			{
				Instance: instanceURL,
			},
		},
	}

	_, err := p.computeService.InstanceGroups.RemoveInstances(
		p.options.ProjectID,
		req.Zone,
		*req.LBConfig.InstanceGroupName,
		instancesRemoveRequest,
	).Context(ctx).Do()

	if err != nil {
		p.logger.Error("Failed to deregister instance from instance group",
			"provider_instance_id", req.ProviderInstanceID,
			"instance_group", *req.LBConfig.InstanceGroupName,
			"zone", req.Zone,
			"error", err)
		return fmt.Errorf("deregistering instance from instance group: %w", err)
	}

	p.logger.Info("Successfully deregistered instance from instance group",
		"provider_instance_id", req.ProviderInstanceID,
		"instance_group", *req.LBConfig.InstanceGroupName,
		"zone", req.Zone)

	return nil
}

// ListLBInstances lists all instances currently registered with a Google Cloud instance group
func (p *Provider) ListLBInstances(ctx context.Context, req provider.ListLBInstancesRequest) ([]string, error) {
	if req.LBConfig.InstanceGroupName == nil || *req.LBConfig.InstanceGroupName == "" {
		return nil, fmt.Errorf("instance group name is required for listing Google Cloud instance group instances")
	}
	if req.Zone == "" {
		return nil, fmt.Errorf("zone is required for listing Google Cloud instance group instances")
	}

	p.logger.Debug("Listing instances in Google Cloud instance group",
		"instance_group", *req.LBConfig.InstanceGroupName,
		"zone", req.Zone)

	listRequest := &computev1.InstanceGroupsListInstancesRequest{}

	result, err := p.computeService.InstanceGroups.ListInstances(
		p.options.ProjectID,
		req.Zone,
		*req.LBConfig.InstanceGroupName,
		listRequest,
	).Context(ctx).Do()

	if err != nil {
		p.logger.Error("Failed to list instance group instances",
			"instance_group", *req.LBConfig.InstanceGroupName,
			"zone", req.Zone,
			"error", err)
		return nil, fmt.Errorf("listing instance group instances: %w", err)
	}

	var instanceIDs []string
	for _, item := range result.Items {
		if item.Instance != "" {
			instanceIDs = append(instanceIDs, extractInstanceIDFromURL(item.Instance))
		}
	}

	p.logger.Debug("Listed instances in instance group",
		"instance_group", *req.LBConfig.InstanceGroupName,
		"zone", req.Zone,
		"count", len(instanceIDs))

	return instanceIDs, nil
}

// extractInstanceIDFromURL extracts the instance ID from a Google Cloud instance URL
// Example: projects/my-project/zones/us-central1-a/instances/my-instance -> my-instance
func extractInstanceIDFromURL(url string) string {
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' {
			return url[i+1:]
		}
	}
	return url
}

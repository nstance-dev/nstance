// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/api/compute/v1"

	"github.com/nstance-dev/nstance/v2/internal/server/infra/provider"
)

// EnsureNATRoute points a tenant-tagged NAT44 or NAT64 route at the requested VM.
func (p *Provider) EnsureNATRoute(ctx context.Context, req provider.NATRouteRequest) error {
	name, description := natRouteIdentity(req)
	route, err := p.computeService.Routes.Get(p.options.ProjectID, name).Context(ctx).Do()
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("get route %s: %w", name, err)
	}
	if route != nil && (route.Description != description || route.DestRange != req.DestinationCIDR) {
		return fmt.Errorf("route %s is not this tenant subnet's translation route", name)
	}
	if req.PublicAddress != nil {
		if err := p.movePublicAddress(ctx, req); err != nil {
			return err
		}
	}
	if route != nil {
		if strings.HasSuffix(route.NextHopInstance, "/instances/"+req.ProviderInstanceID) {
			return nil
		}
		operation, err := p.computeService.Routes.Delete(p.options.ProjectID, name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("delete previous route %s: %w", name, err)
		}
		if _, err := p.computeService.GlobalOperations.Wait(p.options.ProjectID, operation.Name).Context(ctx).Do(); err != nil {
			return fmt.Errorf("wait for route %s deletion: %w", name, err)
		}
	}
	subnet, err := p.computeService.Subnetworks.Get(p.options.ProjectID, p.config.Region, req.InstanceSubnetID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("get subnet %s: %w", req.InstanceSubnetID, err)
	}
	if req.InstanceTag == "" {
		req.InstanceTag = name
	}
	operation, err := p.computeService.Routes.Insert(p.options.ProjectID, &compute.Route{
		Name:            name,
		Description:     description,
		DestRange:       req.DestinationCIDR,
		Network:         subnet.Network,
		Priority:        800,
		Tags:            []string{req.InstanceTag},
		NextHopInstance: fmt.Sprintf("zones/%s/instances/%s", p.config.Zone, req.ProviderInstanceID),
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("create route %s: %w", name, err)
	}
	if _, err := p.computeService.GlobalOperations.Wait(p.options.ProjectID, operation.Name).Context(ctx).Do(); err != nil {
		return fmt.Errorf("wait for route %s creation: %w", name, err)
	}
	return nil
}

// RemoveNATRoute removes only the named tenant translation route, leaving other destinations alone.
func (p *Provider) RemoveNATRoute(ctx context.Context, req provider.NATRouteRequest) error {
	name, description := natRouteIdentity(req)
	route, err := p.computeService.Routes.Get(p.options.ProjectID, name).Context(ctx).Do()
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get route %s: %w", name, err)
	}
	if route.Description != description || route.DestRange != req.DestinationCIDR {
		return nil
	}
	operation, err := p.computeService.Routes.Delete(p.options.ProjectID, name).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("delete route %s: %w", name, err)
	}
	if _, err := p.computeService.GlobalOperations.Wait(p.options.ProjectID, operation.Name).Context(ctx).Do(); err != nil {
		return fmt.Errorf("wait for route %s deletion: %w", name, err)
	}
	return nil
}

// natRouteIdentity returns the provider name and ownership marker for a NAT route.
func natRouteIdentity(req provider.NATRouteRequest) (string, string) {
	name := provider.NATNetworkTag(req.ClusterID, req.Tenant, req.InstanceSubnetID)
	description := fmt.Sprintf("nstance:%s:%s:%s", req.ClusterID, req.Tenant, req.InstanceSubnetID)
	if req.DestinationCIDR == "64:ff9b::/96" {
		return name + "-nat64", description + ":nat64"
	}
	return name, description
}

// movePublicAddress moves a reserved external address to the target instance.
func (p *Provider) movePublicAddress(ctx context.Context, req provider.NATRouteRequest) error {
	address := req.PublicAddress.IPv4
	if address == "" {
		return fmt.Errorf("fixed public IPv4 address is empty")
	}
	if req.PreviousProviderInstanceID != "" && req.PreviousProviderInstanceID != req.ProviderInstanceID {
		if err := p.removeAccessConfig(ctx, req.PreviousProviderInstanceID, address, false); err != nil {
			return fmt.Errorf("release fixed public IPv4 address from previous instance: %w", err)
		}
	}
	instance, err := p.computeService.Instances.Get(p.options.ProjectID, p.config.Zone, req.ProviderInstanceID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("get NAT instance %s: %w", req.ProviderInstanceID, err)
	}
	if current, _ := accessConfig(instance); current != nil && current.NatIP == address {
		return nil
	}
	if err := p.removeAccessConfig(ctx, req.ProviderInstanceID, "", true); err != nil {
		return fmt.Errorf("remove temporary public IPv4 address: %w", err)
	}
	operation, err := p.computeService.Instances.AddAccessConfig(
		p.options.ProjectID,
		p.config.Zone,
		req.ProviderInstanceID,
		"nic0",
		&compute.AccessConfig{Name: "External NAT", Type: "ONE_TO_ONE_NAT", NatIP: address, NetworkTier: "PREMIUM"},
	).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("assign fixed public IPv4 address %s: %w", address, err)
	}
	if _, err := p.computeService.ZoneOperations.Wait(p.options.ProjectID, p.config.Zone, operation.Name).Context(ctx).Do(); err != nil {
		return fmt.Errorf("wait for fixed public IPv4 assignment: %w", err)
	}
	return nil
}

// removeAccessConfig removes a matching or arbitrary external IPv4 configuration.
func (p *Provider) removeAccessConfig(ctx context.Context, instanceID, address string, any bool) error {
	instance, err := p.computeService.Instances.Get(p.options.ProjectID, p.config.Zone, instanceID).Context(ctx).Do()
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get instance %s: %w", instanceID, err)
	}
	access, interfaceName := accessConfig(instance)
	if access == nil || (!any && access.NatIP != address) {
		return nil
	}
	operation, err := p.computeService.Instances.DeleteAccessConfig(
		p.options.ProjectID, p.config.Zone, instanceID, access.Name, interfaceName,
	).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("delete access config from instance %s: %w", instanceID, err)
	}
	if _, err := p.computeService.ZoneOperations.Wait(p.options.ProjectID, p.config.Zone, operation.Name).Context(ctx).Do(); err != nil {
		return fmt.Errorf("wait for access config removal: %w", err)
	}
	return nil
}

// accessConfig returns the instance's one-to-one NAT access configuration and interface.
func accessConfig(instance *compute.Instance) (*compute.AccessConfig, string) {
	for _, networkInterface := range instance.NetworkInterfaces {
		for _, access := range networkInterface.AccessConfigs {
			if access.Type == "ONE_TO_ONE_NAT" {
				return access, networkInterface.Name
			}
		}
	}
	return nil, ""
}

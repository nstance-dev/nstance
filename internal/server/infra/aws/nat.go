// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// EnsureNATRoute activates the requested NAT interface and points the instance
// subnet's owned IPv4 default or NAT64 prefix route at it.
func (p *Provider) EnsureNATRoute(ctx context.Context, req provider.NATRouteRequest) error {
	target, err := p.natTargetInterface(ctx, req)
	if err != nil {
		return err
	}
	managed, err := p.permittedNATInterfaces(ctx, req)
	if err != nil {
		return err
	}
	return p.ensureNATRoute(ctx, req, target, managed)
}

// ensureNATRoute creates or replaces an owned default route with the target ENI.
func (p *Provider) ensureNATRoute(ctx context.Context, req provider.NATRouteRequest, target string, managed []string) error {
	routeTable, route, err := p.natRoute(ctx, req)
	if err != nil {
		return err
	}
	if route != nil && aws.ToString(route.NetworkInterfaceId) == target {
		return nil
	}
	if route != nil && !slices.Contains(managed, aws.ToString(route.NetworkInterfaceId)) {
		return fmt.Errorf("subnet %s default route is not Nstance-managed", req.InstanceSubnetID)
	}
	if route == nil {
		input := &ec2.CreateRouteInput{
			RouteTableId:       routeTable.RouteTableId,
			NetworkInterfaceId: aws.String(target),
		}
		setCreateRouteDestination(input, req.DestinationCIDR)
		_, err = p.routeClient.CreateRoute(ctx, input)
	} else {
		input := &ec2.ReplaceRouteInput{
			RouteTableId:       routeTable.RouteTableId,
			NetworkInterfaceId: aws.String(target),
		}
		setReplaceRouteDestination(input, req.DestinationCIDR)
		_, err = p.routeClient.ReplaceRoute(ctx, input)
	}
	if err != nil {
		return fmt.Errorf("set subnet %s default route: %w", req.InstanceSubnetID, err)
	}
	return nil
}

// RemoveNATRoute deletes a subnet default route only while it targets a managed ENI.
func (p *Provider) RemoveNATRoute(ctx context.Context, req provider.NATRouteRequest) error {
	managed, err := p.permittedNATInterfaces(ctx, req)
	if err != nil {
		return err
	}
	routeTable, route, err := p.natRoute(ctx, req)
	if err != nil || route == nil {
		return err
	}
	if !slices.Contains(managed, aws.ToString(route.NetworkInterfaceId)) {
		return nil
	}
	input := &ec2.DeleteRouteInput{RouteTableId: routeTable.RouteTableId}
	setDeleteRouteDestination(input, req.DestinationCIDR)
	_, err = p.routeClient.DeleteRoute(ctx, input)
	if err != nil {
		return fmt.Errorf("delete subnet %s default route: %w", req.InstanceSubnetID, err)
	}
	return nil
}

// natTargetInterface prepares an instance's primary ENI for NAT traffic.
func (p *Provider) natTargetInterface(ctx context.Context, req provider.NATRouteRequest) (string, error) {
	interfaceID, err := p.primaryNetworkInterface(ctx, req.ProviderInstanceID)
	if err != nil {
		return "", err
	}
	if err := p.disableSourceDestinationCheck(ctx, interfaceID); err != nil {
		return "", err
	}
	if req.PublicAddress != nil {
		if err := p.assignPublicAddress(ctx, req.ProviderInstanceID, *req.PublicAddress); err != nil {
			return "", err
		}
	}
	return interfaceID, nil
}

// permittedNATInterfaces resolves the current and previous NAT instances' primary ENIs.
func (p *Provider) permittedNATInterfaces(ctx context.Context, req provider.NATRouteRequest) ([]string, error) {
	instanceIDs := []string{req.ProviderInstanceID, req.PreviousProviderInstanceID}
	managed := make([]string, 0, len(instanceIDs))
	for i, instanceID := range instanceIDs {
		if instanceID == "" || i == 1 && instanceID == req.ProviderInstanceID {
			continue
		}
		interfaceID, err := p.primaryNetworkInterface(ctx, instanceID)
		if err != nil {
			return nil, fmt.Errorf("resolve managed NAT instance %s: %w", instanceID, err)
		}
		managed = append(managed, interfaceID)
	}
	return managed, nil
}

// primaryNetworkInterface returns an instance's device-index-zero ENI.
func (p *Provider) primaryNetworkInterface(ctx context.Context, instanceID string) (string, error) {
	result, err := p.natClient.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return "", fmt.Errorf("describe instance %s: %w", instanceID, err)
	}
	if len(result.Reservations) != 1 || len(result.Reservations[0].Instances) != 1 {
		return "", fmt.Errorf("instance %s returned an unexpected result", instanceID)
	}
	for _, networkInterface := range result.Reservations[0].Instances[0].NetworkInterfaces {
		if networkInterface.Attachment != nil && aws.ToInt32(networkInterface.Attachment.DeviceIndex) == 0 {
			return aws.ToString(networkInterface.NetworkInterfaceId), nil
		}
	}
	return "", fmt.Errorf("instance %s has no primary network interface", instanceID)
}

// disableSourceDestinationCheck permits an ENI to forward traffic for other hosts.
func (p *Provider) disableSourceDestinationCheck(ctx context.Context, interfaceID string) error {
	_, err := p.natClient.ModifyNetworkInterfaceAttribute(ctx, &ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: aws.String(interfaceID),
		SourceDestCheck:    &types.AttributeBooleanValue{Value: aws.Bool(false)},
	})
	if err != nil {
		return fmt.Errorf("disable source/destination check on %s: %w", interfaceID, err)
	}
	return nil
}

// assignPublicAddress reassociates an Elastic IP with the target instance.
func (p *Provider) assignPublicAddress(ctx context.Context, instanceID string, address provider.PublicAddress) error {
	if address.AllocationID == "" {
		return fmt.Errorf("fixed public IPv4 address requires an allocation ID on AWS")
	}
	_, err := p.natClient.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId:       aws.String(address.AllocationID),
		InstanceId:         aws.String(instanceID),
		AllowReassociation: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("assign fixed public IPv4 address %s: %w", address.IPv4, err)
	}
	return nil
}

// natRoute returns the cluster-owned route table and requested NAT route.
func (p *Provider) natRoute(ctx context.Context, req provider.NATRouteRequest) (*types.RouteTable, *types.Route, error) {
	result, err := p.routeClient.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{Filters: []types.Filter{{
		Name: aws.String("association.subnet-id"), Values: []string{req.InstanceSubnetID},
	}}})
	if err != nil {
		return nil, nil, fmt.Errorf("describe subnet %s route table: %w", req.InstanceSubnetID, err)
	}
	if len(result.RouteTables) != 1 {
		return nil, nil, fmt.Errorf("subnet %s has %d associated route tables", req.InstanceSubnetID, len(result.RouteTables))
	}
	routeTable := &result.RouteTables[0]
	owned := false
	for _, tag := range routeTable.Tags {
		owned = owned || aws.ToString(tag.Key) == tagClusterID && aws.ToString(tag.Value) == req.ClusterID
	}
	if !owned {
		return nil, nil, fmt.Errorf("subnet %s route table is not owned by cluster %s", req.InstanceSubnetID, req.ClusterID)
	}
	for i := range routeTable.Routes {
		if routeDestination(&routeTable.Routes[i]) == req.DestinationCIDR {
			return routeTable, &routeTable.Routes[i], nil
		}
	}
	return routeTable, nil, nil
}

// routeDestination returns either the IPv4 or IPv6 destination of a route.
func routeDestination(route *types.Route) string {
	if route.DestinationCidrBlock != nil {
		return aws.ToString(route.DestinationCidrBlock)
	}
	return aws.ToString(route.DestinationIpv6CidrBlock)
}

// setCreateRouteDestination sets the appropriate address-family field.
func setCreateRouteDestination(input *ec2.CreateRouteInput, destination string) {
	if destination == "64:ff9b::/96" {
		input.DestinationIpv6CidrBlock = aws.String(destination)
	} else {
		input.DestinationCidrBlock = aws.String(destination)
	}
}

// setReplaceRouteDestination sets the appropriate address-family field.
func setReplaceRouteDestination(input *ec2.ReplaceRouteInput, destination string) {
	if destination == "64:ff9b::/96" {
		input.DestinationIpv6CidrBlock = aws.String(destination)
	} else {
		input.DestinationCidrBlock = aws.String(destination)
	}
}

// setDeleteRouteDestination sets the appropriate address-family field.
func setDeleteRouteDestination(input *ec2.DeleteRouteInput, destination string) {
	if destination == "64:ff9b::/96" {
		input.DestinationIpv6CidrBlock = aws.String(destination)
	} else {
		input.DestinationCidrBlock = aws.String(destination)
	}
}

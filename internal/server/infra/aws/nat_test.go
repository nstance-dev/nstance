// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/nstance-dev/nstance/v2/internal/server/infra/provider"
)

// testRouteClient applies route changes to one configured route table.
type testRouteClient struct {
	routeTable types.RouteTable
}

// testNATClient records EC2 operations against a synthetic primary ENI.
type testNATClient struct {
	associatedInstance string
	allocationID       string
	modifiedInterface  string
	deletedInstance    string
}

// DescribeInstances returns one instance with a synthetic primary ENI.
func (c *testNATClient) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if input.InstanceIds[0] == c.deletedInstance {
		return nil, provider.ErrInstanceNotFound
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{{
			Instances: []types.Instance{{
				InstanceId: aws.String(input.InstanceIds[0]),
				NetworkInterfaces: []types.InstanceNetworkInterface{{
					NetworkInterfaceId: aws.String("eni-primary"),
					Attachment:         &types.InstanceNetworkInterfaceAttachment{DeviceIndex: aws.Int32(0)},
				}},
			}},
		}},
	}, nil
}

// ModifyNetworkInterfaceAttribute records the modified ENI.
func (c *testNATClient) ModifyNetworkInterfaceAttribute(_ context.Context, input *ec2.ModifyNetworkInterfaceAttributeInput, _ ...func(*ec2.Options)) (*ec2.ModifyNetworkInterfaceAttributeOutput, error) {
	c.modifiedInterface = aws.ToString(input.NetworkInterfaceId)
	return &ec2.ModifyNetworkInterfaceAttributeOutput{}, nil
}

// AssociateAddress records the target instance and Elastic IP allocation.
func (c *testNATClient) AssociateAddress(_ context.Context, input *ec2.AssociateAddressInput, _ ...func(*ec2.Options)) (*ec2.AssociateAddressOutput, error) {
	c.associatedInstance = aws.ToString(input.InstanceId)
	c.allocationID = aws.ToString(input.AllocationId)
	return &ec2.AssociateAddressOutput{}, nil
}

// DescribeRouteTables returns the configured route table.
func (c *testRouteClient) DescribeRouteTables(context.Context, *ec2.DescribeRouteTablesInput, ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	return &ec2.DescribeRouteTablesOutput{RouteTables: []types.RouteTable{c.routeTable}}, nil
}

// CreateRoute appends the requested translation route.
func (c *testRouteClient) CreateRoute(_ context.Context, input *ec2.CreateRouteInput, _ ...func(*ec2.Options)) (*ec2.CreateRouteOutput, error) {
	c.routeTable.Routes = append(c.routeTable.Routes, types.Route{
		DestinationCidrBlock: input.DestinationCidrBlock, DestinationIpv6CidrBlock: input.DestinationIpv6CidrBlock,
		NetworkInterfaceId: input.NetworkInterfaceId,
	})
	return &ec2.CreateRouteOutput{}, nil
}

// ReplaceRoute updates only the requested destination's ENI.
func (c *testRouteClient) ReplaceRoute(_ context.Context, input *ec2.ReplaceRouteInput, _ ...func(*ec2.Options)) (*ec2.ReplaceRouteOutput, error) {
	for i, route := range c.routeTable.Routes {
		if aws.ToString(route.DestinationCidrBlock) == aws.ToString(input.DestinationCidrBlock) && aws.ToString(route.DestinationIpv6CidrBlock) == aws.ToString(input.DestinationIpv6CidrBlock) {
			c.routeTable.Routes[i] = types.Route{
				DestinationCidrBlock: input.DestinationCidrBlock, DestinationIpv6CidrBlock: input.DestinationIpv6CidrBlock,
				NetworkInterfaceId: input.NetworkInterfaceId,
			}
		}
	}
	return &ec2.ReplaceRouteOutput{}, nil
}

// DeleteRoute removes only the requested destination.
func (c *testRouteClient) DeleteRoute(_ context.Context, input *ec2.DeleteRouteInput, _ ...func(*ec2.Options)) (*ec2.DeleteRouteOutput, error) {
	for i, route := range c.routeTable.Routes {
		if aws.ToString(route.DestinationCidrBlock) == aws.ToString(input.DestinationCidrBlock) && aws.ToString(route.DestinationIpv6CidrBlock) == aws.ToString(input.DestinationIpv6CidrBlock) {
			c.routeTable.Routes = append(c.routeTable.Routes[:i], c.routeTable.Routes[i+1:]...)
			break
		}
	}
	return &ec2.DeleteRouteOutput{}, nil
}

// TestEnsureNATRouteRequiresOwnedManagedRoute verifies another cluster's table is never changed.
func TestEnsureNATRouteRequiresOwnedManagedRoute(t *testing.T) {
	client := &testRouteClient{routeTable: types.RouteTable{
		RouteTableId: aws.String("rtb-1"),
		Tags:         []types.Tag{{Key: aws.String(tagClusterID), Value: aws.String("other")}},
		Routes:       []types.Route{{DestinationCidrBlock: aws.String("0.0.0.0/0"), NetworkInterfaceId: aws.String("eni-old")}},
	}}
	p := &Provider{routeClient: client, natClient: &testNATClient{}}
	req := provider.NATRouteRequest{
		ClusterID: "cluster", Tenant: "red", InstanceSubnetID: "subnet-a",
		DestinationCIDR: "0.0.0.0/0", ProviderInstanceID: "i-new",
	}
	if err := p.EnsureNATRoute(context.Background(), req); err == nil {
		t.Fatal("another cluster's route table was modified")
	}
	if err := p.RemoveNATRoute(context.Background(), req); err == nil {
		t.Fatal("another cluster's route table was accepted for cleanup")
	}
	if len(client.routeTable.Routes) != 1 || aws.ToString(client.routeTable.Routes[0].NetworkInterfaceId) != "eni-old" {
		t.Fatalf("foreign routes changed: %#v", client.routeTable.Routes)
	}
}

// TestNATTargetUsesPrimaryInterface verifies fixed egress reassigns the EIP
// without introducing a secondary guest interface.
func TestNATTargetUsesPrimaryInterface(t *testing.T) {
	client := &testNATClient{}
	routes := &testRouteClient{routeTable: types.RouteTable{
		RouteTableId: aws.String("rtb-1"),
		Tags:         []types.Tag{{Key: aws.String(tagClusterID), Value: aws.String("cluster")}},
	}}
	p := &Provider{natClient: client, routeClient: routes}
	err := p.EnsureNATRoute(context.Background(), provider.NATRouteRequest{
		ClusterID: "cluster", InstanceSubnetID: "subnet-a", DestinationCIDR: "0.0.0.0/0",
		ProviderInstanceID: "i-new",
		PublicAddress:      &provider.PublicAddress{IPv4: "192.0.2.1", AllocationID: "eipalloc-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes.routeTable.Routes) != 1 || aws.ToString(routes.routeTable.Routes[0].NetworkInterfaceId) != "eni-primary" || client.modifiedInterface != "eni-primary" {
		t.Fatalf("routes=%#v modified=%q, want primary ENI", routes.routeTable.Routes, client.modifiedInterface)
	}
	if client.associatedInstance != "i-new" || client.allocationID != "eipalloc-1" {
		t.Fatalf("association instance=%q allocation=%q", client.associatedInstance, client.allocationID)
	}
}

// TestNATRouteRecoversDeletedInstance verifies translation routes can be corrected
// without discovering the old VM, while peering and native IPv6 routes survive.
func TestNATRouteRecoversDeletedInstance(t *testing.T) {
	for _, destination := range []string{"0.0.0.0/0", "64:ff9b::/96"} {
		for _, nextHop := range []string{"eni-deleted", "eni-unrelated", "nat-gateway"} {
			t.Run(destination+"/"+nextHop, func(t *testing.T) {
				translation := types.Route{NetworkInterfaceId: aws.String(nextHop), State: types.RouteStateBlackhole}
				if destination == "0.0.0.0/0" {
					translation.DestinationCidrBlock = aws.String(destination)
				} else {
					translation.DestinationIpv6CidrBlock = aws.String(destination)
				}
				if nextHop == "nat-gateway" {
					translation.NetworkInterfaceId = nil
					translation.NatGatewayId = aws.String("nat-1")
				}
				unrelated := []types.Route{
					{DestinationCidrBlock: aws.String("10.43.0.0/16"), VpcPeeringConnectionId: aws.String("pcx-1")},
					{DestinationIpv6CidrBlock: aws.String("::/0"), EgressOnlyInternetGatewayId: aws.String("eigw-1")},
				}
				client := &testRouteClient{routeTable: types.RouteTable{
					RouteTableId: aws.String("rtb-1"),
					Tags:         []types.Tag{{Key: aws.String(tagClusterID), Value: aws.String("cluster")}},
					Routes:       append(append([]types.Route(nil), unrelated...), translation),
				}}
				p := &Provider{routeClient: client, natClient: &testNATClient{deletedInstance: "i-old"}}
				req := provider.NATRouteRequest{
					ClusterID: "cluster", Tenant: "red", InstanceSubnetID: "subnet-a",
					DestinationCIDR: destination, ProviderInstanceID: "i-new",
				}
				if err := p.EnsureNATRoute(context.Background(), req); err != nil {
					t.Fatal(err)
				}
				if len(client.routeTable.Routes) != 3 || !reflect.DeepEqual(client.routeTable.Routes[:2], unrelated) || aws.ToString(client.routeTable.Routes[2].NetworkInterfaceId) != "eni-primary" {
					t.Fatalf("unexpected routes after cutover: %#v", client.routeTable.Routes)
				}
				// Removing the unused NAT service must not need its dead VM's ENI.
				req.ProviderInstanceID = "i-old"
				if err := p.RemoveNATRoute(context.Background(), req); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(client.routeTable.Routes, unrelated) {
					t.Fatalf("unexpected routes after cleanup: %#v", client.routeTable.Routes)
				}
			})
		}
	}
}

// TestNATCleanupPreservesProviderGateway verifies switching to provider NAT leaves its route intact.
func TestNATCleanupPreservesProviderGateway(t *testing.T) {
	client := &testRouteClient{routeTable: types.RouteTable{
		RouteTableId: aws.String("rtb-1"),
		Tags:         []types.Tag{{Key: aws.String(tagClusterID), Value: aws.String("cluster")}},
		Routes:       []types.Route{{DestinationCidrBlock: aws.String("0.0.0.0/0"), NatGatewayId: aws.String("nat-1")}},
	}}
	p := &Provider{routeClient: client}
	if err := p.RemoveNATRoute(context.Background(), provider.NATRouteRequest{
		ClusterID: "cluster", InstanceSubnetID: "subnet-a", DestinationCIDR: "0.0.0.0/0",
	}); err != nil {
		t.Fatal(err)
	}
	if len(client.routeTable.Routes) != 1 || aws.ToString(client.routeTable.Routes[0].NatGatewayId) != "nat-1" {
		t.Fatalf("provider NAT route changed: %#v", client.routeTable.Routes)
	}
}

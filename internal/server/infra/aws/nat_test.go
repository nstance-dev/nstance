// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// testRouteClient records replacements against one configured route table.
type testRouteClient struct {
	routeTable types.RouteTable
	replaced   string
}

// testNATClient records EC2 operations against a synthetic primary ENI.
type testNATClient struct {
	associatedInstance string
	allocationID       string
	modifiedInterface  string
}

// DescribeInstances returns one instance with a synthetic primary ENI.
func (*testNATClient) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
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

// CreateRoute records no state because these tests begin with a default route.
func (*testRouteClient) CreateRoute(context.Context, *ec2.CreateRouteInput, ...func(*ec2.Options)) (*ec2.CreateRouteOutput, error) {
	return &ec2.CreateRouteOutput{}, nil
}

// ReplaceRoute records the requested ENI.
func (c *testRouteClient) ReplaceRoute(_ context.Context, input *ec2.ReplaceRouteInput, _ ...func(*ec2.Options)) (*ec2.ReplaceRouteOutput, error) {
	c.replaced = aws.ToString(input.NetworkInterfaceId)
	return &ec2.ReplaceRouteOutput{}, nil
}

// DeleteRoute records no state because these tests exercise replacement.
func (*testRouteClient) DeleteRoute(context.Context, *ec2.DeleteRouteInput, ...func(*ec2.Options)) (*ec2.DeleteRouteOutput, error) {
	return &ec2.DeleteRouteOutput{}, nil
}

// TestEnsureNATRouteRequiresOwnedManagedRoute verifies stale reconciliation
// cannot replace either another cluster's route or a Cloud NAT next hop.
func TestEnsureNATRouteRequiresOwnedManagedRoute(t *testing.T) {
	client := &testRouteClient{routeTable: types.RouteTable{
		RouteTableId: aws.String("rtb-1"),
		Tags:         []types.Tag{{Key: aws.String(tagClusterID), Value: aws.String("cluster")}},
		Routes:       []types.Route{{DestinationCidrBlock: aws.String("0.0.0.0/0"), NetworkInterfaceId: aws.String("eni-old")}},
	}}
	p := &Provider{routeClient: client}
	req := provider.NATRouteRequest{
		ClusterID:        "cluster",
		Tenant:           "red",
		InstanceSubnetID: "subnet-a",
	}
	managed := []string{"eni-old", "eni-new"}
	if err := p.ensureNATRoute(context.Background(), req, "eni-new", managed); err != nil {
		t.Fatal(err)
	}
	if client.replaced != "eni-new" {
		t.Fatalf("replacement = %q, want eni-new", client.replaced)
	}
	client.replaced = ""
	client.routeTable.Routes[0] = types.Route{DestinationCidrBlock: aws.String("0.0.0.0/0"), NatGatewayId: aws.String("nat-1")}
	if err := p.ensureNATRoute(context.Background(), req, "eni-new", managed); err == nil {
		t.Fatal("Cloud NAT route was replaced")
	}
	client.routeTable.Tags[0].Value = aws.String("other")
	if err := p.ensureNATRoute(context.Background(), req, "eni-new", managed); err == nil {
		t.Fatal("another cluster's route table was modified")
	}
}

// TestNATTargetUsesPrimaryInterface verifies fixed egress reassigns the EIP
// without introducing a secondary guest interface.
func TestNATTargetUsesPrimaryInterface(t *testing.T) {
	client := &testNATClient{}
	p := &Provider{natClient: client}
	target, err := p.natTargetInterface(context.Background(), provider.NATRouteRequest{
		ProviderInstanceID: "i-new",
		PublicAddress:      &provider.PublicAddress{IPv4: "192.0.2.1", AllocationID: "eipalloc-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target != "eni-primary" || client.modifiedInterface != "eni-primary" {
		t.Fatalf("target=%q modified=%q, want primary ENI", target, client.modifiedInterface)
	}
	if client.associatedInstance != "i-new" || client.allocationID != "eipalloc-1" {
		t.Fatalf("association instance=%q allocation=%q", client.associatedInstance, client.allocationID)
	}
}

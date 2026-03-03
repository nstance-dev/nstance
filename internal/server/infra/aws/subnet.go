// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// CheckSubnetCapacity checks if a subnet has available IP addresses
func (p *Provider) CheckSubnetCapacity(ctx context.Context, subnetID string) (bool, error) {
	result, err := p.ec2Client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetID},
	})
	if err != nil {
		return false, fmt.Errorf("failed to describe subnet %s: %w", subnetID, err)
	}

	if len(result.Subnets) == 0 {
		return false, fmt.Errorf("subnet not found: %s", subnetID)
	}

	subnet := result.Subnets[0]
	availableIPs := aws.ToInt32(subnet.AvailableIpAddressCount)

	// Consider subnet to have capacity if it has more than 10 available IPs
	hasCapacity := availableIPs > 10
	p.logger.Debug("Checked subnet capacity",
		"subnet_id", subnetID,
		"available_ips", availableIPs,
		"has_capacity", hasCapacity)

	return hasCapacity, nil
}

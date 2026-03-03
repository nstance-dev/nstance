// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// AssignLeaderNetwork attaches the specified ENI to the specified instance
func (p *Provider) AssignLeaderNetwork(ctx context.Context, providerInstanceID string, ln provider.LeaderNetwork) error {
	eniID := ln.InterfaceID
	p.logger.Info("Assigning leader network to instance", "eniID", eniID, "instanceID", providerInstanceID)

	// First, check if the ENI is already attached to the target instance
	describeResp, err := p.ec2Client.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{eniID},
	})
	if err != nil {
		return fmt.Errorf("failed to describe ENI %s: %w", eniID, err)
	}

	if len(describeResp.NetworkInterfaces) == 0 {
		return fmt.Errorf("ENI %s not found", eniID)
	}

	eni := describeResp.NetworkInterfaces[0]

	// If already attached to the target instance, nothing to do
	if eni.Attachment != nil && aws.ToString(eni.Attachment.InstanceId) == providerInstanceID {
		p.logger.Info("ENI already attached to target instance", "eniID", eniID)
		return nil
	}

	// If attached to another instance, detach it first
	if eni.Attachment != nil {
		attachedInstanceID := aws.ToString(eni.Attachment.InstanceId)
		attachmentID := aws.ToString(eni.Attachment.AttachmentId)

		p.logger.Info("Detaching ENI from previous instance",
			"eniID", eniID,
			"previousInstanceID", attachedInstanceID,
			"attachmentID", attachmentID)

		_, err = p.ec2Client.DetachNetworkInterface(ctx, &ec2.DetachNetworkInterfaceInput{
			AttachmentId: aws.String(attachmentID),
			Force:        aws.Bool(true), // Force detachment for faster failover
		})
		if err != nil {
			return fmt.Errorf("failed to detach ENI from instance %s: %w", attachedInstanceID, err)
		}

		// Wait for detachment to complete
		if err := p.waitForENIDetachment(ctx, eniID); err != nil {
			return fmt.Errorf("failed waiting for ENI detachment: %w", err)
		}
	}

	// Attach to the target instance
	attachResp, err := p.ec2Client.AttachNetworkInterface(ctx, &ec2.AttachNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniID),
		InstanceId:         aws.String(providerInstanceID),
		DeviceIndex:        aws.Int32(1), // Use device index 1 (0 is usually primary interface)
	})
	if err != nil {
		return fmt.Errorf("failed to attach ENI to instance %s: %w", providerInstanceID, err)
	}

	p.logger.Info("ENI attachment initiated",
		"eniID", eniID,
		"instanceID", providerInstanceID,
		"attachmentID", aws.ToString(attachResp.AttachmentId))

	// Wait for attachment to complete at AWS level
	return p.waitForENIAttachment(ctx, eniID)
}

// ReleaseLeaderNetwork detaches the specified NetworkInterface from the specified instance
func (p *Provider) ReleaseLeaderNetwork(ctx context.Context, providerInstanceID string, ln provider.LeaderNetwork) error {
	networkInterfaceID := ln.InterfaceID
	p.logger.Info("Releasing leader network from instance", "networkInterfaceID", networkInterfaceID, "instanceID", providerInstanceID)

	// Get current attachment info
	describeResp, err := p.ec2Client.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{networkInterfaceID},
	})
	if err != nil {
		return fmt.Errorf("failed to describe NetworkInterface %s: %w", networkInterfaceID, err)
	}

	if len(describeResp.NetworkInterfaces) == 0 {
		return fmt.Errorf("NetworkInterface %s not found", networkInterfaceID)
	}

	eni := describeResp.NetworkInterfaces[0]

	// If not attached, nothing to do
	if eni.Attachment == nil {
		p.logger.Info("NetworkInterface is not attached", "networkInterfaceID", networkInterfaceID)
		return nil
	}

	// If attached to a different instance, nothing to do
	if aws.ToString(eni.Attachment.InstanceId) != providerInstanceID {
		p.logger.Info("NetworkInterface attached to different instance",
			"networkInterfaceID", networkInterfaceID,
			"attachedInstanceID", aws.ToString(eni.Attachment.InstanceId))
		return nil
	}

	// Detach from this instance
	attachmentID := aws.ToString(eni.Attachment.AttachmentId)
	_, err = p.ec2Client.DetachNetworkInterface(ctx, &ec2.DetachNetworkInterfaceInput{
		AttachmentId: aws.String(attachmentID),
	})
	if err != nil {
		return fmt.Errorf("failed to detach NetworkInterface: %w", err)
	}

	p.logger.Info("NetworkInterface detachment initiated", "networkInterfaceID", networkInterfaceID, "attachmentID", attachmentID)
	return p.waitForENIDetachment(ctx, networkInterfaceID)
}

// waitForENIAttachment waits for ENI to be attached (status becomes "in-use")
func (p *Provider) waitForENIAttachment(ctx context.Context, eniID string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := time.After(2 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for ENI %s to attach", eniID)
		case <-ticker.C:
			output, err := p.ec2Client.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
				NetworkInterfaceIds: []string{eniID},
			})
			if err != nil {
				return fmt.Errorf("failed to describe ENI: %w", err)
			}
			if len(output.NetworkInterfaces) == 0 {
				return fmt.Errorf("ENI %s not found", eniID)
			}
			eni := output.NetworkInterfaces[0]
			if eni.Status == types.NetworkInterfaceStatusInUse {
				return nil
			}
		}
	}
}

// waitForENIDetachment waits for ENI to be detached
func (p *Provider) waitForENIDetachment(ctx context.Context, eniID string) error {
	// Poll until ENI is available (detached)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := time.After(2 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for ENI detachment")
		case <-ticker.C:
			resp, err := p.ec2Client.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
				NetworkInterfaceIds: []string{eniID},
			})
			if err != nil {
				return fmt.Errorf("failed to check ENI status: %w", err)
			}

			if len(resp.NetworkInterfaces) > 0 {
				eni := resp.NetworkInterfaces[0]
				if eni.Status == types.NetworkInterfaceStatusAvailable {
					p.logger.Info("ENI detached successfully", "eniID", eniID)
					return nil
				}
			}
		}
	}
}

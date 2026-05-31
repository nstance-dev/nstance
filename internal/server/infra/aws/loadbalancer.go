// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// RegisterWithLB registers an instance with all AWS NLB target groups in the config
func (p *Provider) RegisterWithLB(ctx context.Context, req provider.RegisterLBRequest) error {
	if len(req.LBConfig.TargetGroupArns) == 0 {
		return fmt.Errorf("at least one target group ARN is required for AWS load balancer registration")
	}

	p.logger.Info("Registering instance with AWS target groups",
		"provider_instance_id", req.ProviderInstanceID,
		"target_group_count", len(req.LBConfig.TargetGroupArns))

	for _, tgArn := range req.LBConfig.TargetGroupArns {
		input := &elasticloadbalancingv2.RegisterTargetsInput{
			TargetGroupArn: aws.String(tgArn),
			Targets: []elbv2types.TargetDescription{
				{
					Id: aws.String(req.ProviderInstanceID),
				},
			},
		}

		_, err := p.elbv2Client.RegisterTargets(ctx, input)
		if err != nil {
			p.logger.Error("Failed to register instance with target group",
				"provider_instance_id", req.ProviderInstanceID,
				"target_group_arn", tgArn,
				"error", err)
			return fmt.Errorf("registering instance with target group %s: %w", tgArn, err)
		}

		p.logger.Debug("Registered instance with target group",
			"provider_instance_id", req.ProviderInstanceID,
			"target_group_arn", tgArn)
	}

	p.logger.Info("Successfully registered instance with all target groups",
		"provider_instance_id", req.ProviderInstanceID,
		"target_group_count", len(req.LBConfig.TargetGroupArns))

	return nil
}

// DeregisterFromLB removes an instance from all AWS NLB target groups in the config
func (p *Provider) DeregisterFromLB(ctx context.Context, req provider.DeregisterLBRequest) error {
	if len(req.LBConfig.TargetGroupArns) == 0 {
		return fmt.Errorf("at least one target group ARN is required for AWS load balancer deregistration")
	}

	p.logger.Info("Deregistering instance from AWS target groups",
		"provider_instance_id", req.ProviderInstanceID,
		"target_group_count", len(req.LBConfig.TargetGroupArns))

	var lastErr error
	for _, tgArn := range req.LBConfig.TargetGroupArns {
		input := &elasticloadbalancingv2.DeregisterTargetsInput{
			TargetGroupArn: aws.String(tgArn),
			Targets: []elbv2types.TargetDescription{
				{
					Id: aws.String(req.ProviderInstanceID),
				},
			},
		}

		_, err := p.elbv2Client.DeregisterTargets(ctx, input)
		if err != nil {
			p.logger.Error("Failed to deregister instance from target group",
				"provider_instance_id", req.ProviderInstanceID,
				"target_group_arn", tgArn,
				"error", err)
			lastErr = err
			// Continue trying other target groups even on error
		} else {
			p.logger.Debug("Deregistered instance from target group",
				"provider_instance_id", req.ProviderInstanceID,
				"target_group_arn", tgArn)
		}
	}

	if lastErr != nil {
		return fmt.Errorf("deregistering instance from target groups: %w", lastErr)
	}

	p.logger.Info("Successfully deregistered instance from all target groups",
		"provider_instance_id", req.ProviderInstanceID,
		"target_group_count", len(req.LBConfig.TargetGroupArns))

	return nil
}

// ListLBInstances lists all instances currently registered with the first AWS NLB target group
// (all target groups for the same LB should have the same instances)
func (p *Provider) ListLBInstances(ctx context.Context, req provider.ListLBInstancesRequest) ([]string, error) {
	if len(req.LBConfig.TargetGroupArns) == 0 {
		return nil, fmt.Errorf("at least one target group ARN is required for listing AWS target group instances")
	}

	// Use the first target group to list instances (all should have same membership)
	tgArn := req.LBConfig.TargetGroupArns[0]

	p.logger.Debug("Listing instances in AWS target group",
		"target_group_arn", tgArn)

	input := &elasticloadbalancingv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgArn),
	}

	result, err := p.elbv2Client.DescribeTargetHealth(ctx, input)
	if err != nil {
		p.logger.Error("Failed to list target group instances",
			"target_group_arn", tgArn,
			"error", err)
		return nil, fmt.Errorf("listing target group instances: %w", err)
	}

	var instanceIDs []string
	for _, targetHealth := range result.TargetHealthDescriptions {
		if targetHealth.Target != nil && targetHealth.Target.Id != nil {
			instanceIDs = append(instanceIDs, *targetHealth.Target.Id)
		}
	}

	p.logger.Debug("Listed instances in target group",
		"target_group_arn", tgArn,
		"count", len(instanceIDs))

	return instanceIDs, nil
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

const crossZoneAttribute = "load_balancing.cross_zone.enabled"

// SetLBCrossZone sets and confirms cross-zone load balancing on every configured target group.
func (p *Provider) SetLBCrossZone(ctx context.Context, req provider.SetLBCrossZoneRequest) error {
	if len(req.LBConfig.TargetGroups) == 0 {
		return fmt.Errorf("at least one target group ARN is required for configuring cross-zone load balancing")
	}
	want := strconv.FormatBool(req.Enabled)
	for _, targetGroup := range req.LBConfig.TargetGroups {
		current, err := p.describeCrossZone(ctx, targetGroup.ARN)
		if err != nil {
			return err
		}
		if current == want {
			continue
		}
		_, err = p.elbv2Client.ModifyTargetGroupAttributes(ctx, &elasticloadbalancingv2.ModifyTargetGroupAttributesInput{
			TargetGroupArn: aws.String(targetGroup.ARN),
			Attributes:     []types.TargetGroupAttribute{{Key: aws.String(crossZoneAttribute), Value: aws.String(want)}},
		})
		if err != nil {
			return fmt.Errorf("modifying cross-zone load balancing for target group %s: %w", targetGroup.ARN, err)
		}
		confirmed, err := p.describeCrossZone(ctx, targetGroup.ARN)
		if err != nil {
			return err
		}
		if confirmed != want {
			return fmt.Errorf("cross-zone load balancing for target group %s was not confirmed as %s", targetGroup.ARN, want)
		}
	}
	return nil
}

// describeCrossZone returns the target group's cross-zone load-balancing attribute.
func (p *Provider) describeCrossZone(ctx context.Context, targetGroupARN string) (string, error) {
	result, err := p.elbv2Client.DescribeTargetGroupAttributes(ctx, &elasticloadbalancingv2.DescribeTargetGroupAttributesInput{
		TargetGroupArn: aws.String(targetGroupARN),
	})
	if err != nil {
		return "", fmt.Errorf("describing attributes for target group %s: %w", targetGroupARN, err)
	}
	for _, attribute := range result.Attributes {
		if aws.ToString(attribute.Key) == crossZoneAttribute {
			return aws.ToString(attribute.Value), nil
		}
	}
	return "", nil
}

// RegisterWithLB registers an instance with all AWS NLB target groups in the config
func (p *Provider) RegisterWithLB(ctx context.Context, req provider.RegisterLBRequest) error {
	if len(req.LBConfig.TargetGroups) == 0 {
		return fmt.Errorf("at least one target group ARN is required for AWS load balancer registration")
	}

	p.logger.Info("Registering instance with AWS target groups",
		"provider_instance_id", req.ProviderInstanceID,
		"target_group_count", len(req.LBConfig.TargetGroups))

	for _, targetGroup := range req.LBConfig.TargetGroups {
		input := &elasticloadbalancingv2.RegisterTargetsInput{
			TargetGroupArn: aws.String(targetGroup.ARN),
			Targets: []types.TargetDescription{
				{
					Id:   aws.String(req.ProviderInstanceID),
					Port: aws.Int32(int32(targetPort(targetGroup, req.WakeProxy))),
				},
			},
		}

		_, err := p.elbv2Client.RegisterTargets(ctx, input)
		if err != nil {
			p.logger.Error("Failed to register instance with target group",
				"provider_instance_id", req.ProviderInstanceID,
				"target_group_arn", targetGroup.ARN,
				"error", err)
			return fmt.Errorf("registering instance with target group %s: %w", targetGroup.ARN, err)
		}

		p.logger.Debug("Registered instance with target group",
			"provider_instance_id", req.ProviderInstanceID,
			"target_group_arn", targetGroup.ARN)
	}

	p.logger.Info("Successfully registered instance with all target groups",
		"provider_instance_id", req.ProviderInstanceID,
		"target_group_count", len(req.LBConfig.TargetGroups))

	return nil
}

// DeregisterFromLB removes an instance from all AWS NLB target groups in the config
func (p *Provider) DeregisterFromLB(ctx context.Context, req provider.DeregisterLBRequest) error {
	if len(req.LBConfig.TargetGroups) == 0 {
		return fmt.Errorf("at least one target group ARN is required for AWS load balancer deregistration")
	}

	p.logger.Info("Deregistering instance from AWS target groups",
		"provider_instance_id", req.ProviderInstanceID,
		"target_group_count", len(req.LBConfig.TargetGroups))

	var lastErr error
	for _, targetGroup := range req.LBConfig.TargetGroups {
		input := &elasticloadbalancingv2.DeregisterTargetsInput{
			TargetGroupArn: aws.String(targetGroup.ARN),
			Targets: []types.TargetDescription{
				{
					Id:   aws.String(req.ProviderInstanceID),
					Port: aws.Int32(int32(targetPort(targetGroup, req.WakeProxy))),
				},
			},
		}

		_, err := p.elbv2Client.DeregisterTargets(ctx, input)
		if err != nil {
			p.logger.Error("Failed to deregister instance from target group",
				"provider_instance_id", req.ProviderInstanceID,
				"target_group_arn", targetGroup.ARN,
				"error", err)
			lastErr = err
			// Continue trying other target groups even on error
		} else {
			p.logger.Debug("Deregistered instance from target group",
				"provider_instance_id", req.ProviderInstanceID,
				"target_group_arn", targetGroup.ARN)
		}
	}

	if lastErr != nil {
		return fmt.Errorf("deregistering instance from target groups: %w", lastErr)
	}

	p.logger.Info("Successfully deregistered instance from all target groups",
		"provider_instance_id", req.ProviderInstanceID,
		"target_group_count", len(req.LBConfig.TargetGroups))

	return nil
}

// GetLBTargetState returns the aggregate lifecycle state across every target group.
func (p *Provider) GetLBTargetState(ctx context.Context, req provider.RegisterLBRequest) (provider.LBTargetState, error) {
	if len(req.LBConfig.TargetGroups) == 0 {
		return "", fmt.Errorf("at least one target group ARN is required for target state")
	}
	healthy := 0
	registered := 0
	for _, targetGroup := range req.LBConfig.TargetGroups {
		result, err := p.elbv2Client.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{
			TargetGroupArn: aws.String(targetGroup.ARN),
			Targets: []types.TargetDescription{{
				Id:   aws.String(req.ProviderInstanceID),
				Port: aws.Int32(int32(targetPort(targetGroup, req.WakeProxy))),
			}},
		})
		if err != nil {
			return "", fmt.Errorf("describing target health for %s: %w", targetGroup.ARN, err)
		}
		if len(result.TargetHealthDescriptions) == 0 {
			continue
		}
		state := result.TargetHealthDescriptions[0].TargetHealth
		if state == nil {
			continue
		}
		if state.Reason == types.TargetHealthReasonEnumNotRegistered {
			continue
		}
		registered++
		switch state.State {
		case types.TargetHealthStateEnumDraining:
			return provider.LBTargetDraining, nil
		case types.TargetHealthStateEnumHealthy:
			healthy++
		}
	}
	if registered == 0 {
		return provider.LBTargetDeregistered, nil
	}
	if registered < len(req.LBConfig.TargetGroups) {
		return provider.LBTargetPartial, nil
	}
	if healthy == len(req.LBConfig.TargetGroups) {
		return provider.LBTargetHealthy, nil
	}
	return provider.LBTargetRegistered, nil
}

// targetPort returns the direct or wake-proxy port for a target group.
func targetPort(targetGroup provider.AWSTargetGroupConfig, wakeProxy bool) int {
	if wakeProxy {
		return targetGroup.ProxyPort
	}
	return targetGroup.TargetPort
}

// ListLBInstances lists the union of instances registered with any AWS NLB target group.
func (p *Provider) ListLBInstances(ctx context.Context, req provider.ListLBInstancesRequest) ([]string, error) {
	if len(req.LBConfig.TargetGroups) == 0 {
		return nil, fmt.Errorf("at least one target group ARN is required for listing AWS target group instances")
	}

	instanceIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, targetGroup := range req.LBConfig.TargetGroups {
		p.logger.Debug("Listing instances in AWS target group",
			"target_group_arn", targetGroup.ARN)

		result, err := p.elbv2Client.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{
			TargetGroupArn: aws.String(targetGroup.ARN),
		})
		if err != nil {
			p.logger.Error("Failed to list target group instances",
				"target_group_arn", targetGroup.ARN,
				"error", err)
			return nil, fmt.Errorf("listing target group %s instances: %w", targetGroup.ARN, err)
		}

		for _, targetHealth := range result.TargetHealthDescriptions {
			if targetHealth.Target == nil || targetHealth.Target.Id == nil {
				continue
			}
			instanceID := *targetHealth.Target.Id
			if _, exists := seen[instanceID]; exists {
				continue
			}
			seen[instanceID] = struct{}{}
			instanceIDs = append(instanceIDs, instanceID)
		}
	}

	p.logger.Debug("Listed instances in AWS target groups",
		"target_group_count", len(req.LBConfig.TargetGroups),
		"count", len(instanceIDs))

	return instanceIDs, nil
}

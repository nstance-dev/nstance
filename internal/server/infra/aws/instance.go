// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// CreateInstance creates a new EC2 instance
func (p *Provider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.CreateInstanceResponse, error) {
	p.logger.Info("Creating EC2 instance",
		"instance_id", req.InstanceID,
		"instance_type", req.InstanceType,
		"subnet_id", req.SubnetID)

	// Build RunInstances input
	runInput := &ec2.RunInstancesInput{
		InstanceType: types.InstanceType(req.InstanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		SubnetId:     aws.String(req.SubnetID),
		UserData:     aws.String(base64.StdEncoding.EncodeToString([]byte(req.UserData))),
	}

	// Build tags
	tags := []types.Tag{
		{Key: aws.String(tagInstanceID), Value: aws.String(req.InstanceID)},
		{Key: aws.String(tagNstanceManaged), Value: aws.String("true")},
		{Key: aws.String("Name"), Value: aws.String(fmt.Sprintf("%s-%s-%s", req.ClusterID, req.Group, req.InstanceID))},
	}
	if req.ClusterID != "" {
		tags = append(tags, types.Tag{Key: aws.String(tagClusterID), Value: aws.String(req.ClusterID)})
	}
	if req.Shard != "" {
		tags = append(tags, types.Tag{Key: aws.String(tagShard), Value: aws.String(req.Shard)})
	}
	if req.Group != "" {
		tags = append(tags, types.Tag{Key: aws.String(tagGroup), Value: aws.String(req.Group)})
	}
	if req.Template != "" {
		tags = append(tags, types.Tag{Key: aws.String(tagTemplate), Value: aws.String(req.Template)})
	}
	if req.InstanceKind != "" {
		tags = append(tags, types.Tag{Key: aws.String(tagInstanceKind), Value: aws.String(req.InstanceKind)})
	}
	for key, value := range req.CustomTags {
		tags = append(tags, types.Tag{Key: aws.String(key), Value: aws.String(value)})
	}
	runInput.TagSpecifications = []types.TagSpecification{
		{
			ResourceType: types.ResourceTypeInstance,
			Tags:         tags,
		},
	}

	// Apply provider-specific arguments
	if err := p.applyProviderArgs(runInput, req.Args); err != nil {
		return nil, fmt.Errorf("failed to apply provider arguments: %w", err)
	}

	// Create the instance
	result, err := p.ec2Client.RunInstances(ctx, runInput)
	if err != nil {
		p.logger.Error("Failed to create EC2 instance",
			"instance_id", req.InstanceID,
			"error", err)
		return nil, fmt.Errorf("failed to create EC2 instance: %w", err)
	}

	if len(result.Instances) == 0 {
		return nil, fmt.Errorf("no instances returned from RunInstances")
	}

	ec2Instance := result.Instances[0]

	// Extract response data
	response := &provider.CreateInstanceResponse{
		InstanceID:         req.InstanceID,
		ProviderInstanceID: aws.ToString(ec2Instance.InstanceId),
		Status:             p.convertInstanceState(ec2Instance.State),
		LaunchedAt:         aws.ToTime(ec2Instance.LaunchTime),
		Tags:               req.CustomTags,
	}

	// Add IP addresses when available
	if ec2Instance.PrivateIpAddress != nil {
		response.PrivateIPv4 = aws.ToString(ec2Instance.PrivateIpAddress)
	}
	if ec2Instance.Ipv6Address != nil {
		response.PrivateIPv6 = aws.ToString(ec2Instance.Ipv6Address)
	}

	// Extract hostname from private DNS name (first segment before the first dot)
	if ec2Instance.PrivateDnsName != nil {
		privateDnsName := aws.ToString(ec2Instance.PrivateDnsName)
		if privateDnsName != "" {
			response.Hostname = strings.Split(privateDnsName, ".")[0]
		}
	}

	p.logger.Info("EC2 instance created successfully",
		"instance_id", req.InstanceID,
		"provider_instance_id", response.ProviderInstanceID,
		"status", response.Status)

	return response, nil
}

// DeleteInstance terminates an EC2 instance by provider instance ID
func (p *Provider) DeleteInstance(ctx context.Context, instanceID, providerInstanceID string) error {
	p.logger.Info("Deleting EC2 instance", "provider_instance_id", providerInstanceID)

	_, err := p.ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{providerInstanceID},
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidInstanceID.NotFound" {
			p.logger.Info("EC2 instance already terminated", "provider_instance_id", providerInstanceID)
			return nil
		}
		p.logger.Error("Failed to terminate EC2 instance",
			"provider_instance_id", providerInstanceID,
			"error", err)
		return fmt.Errorf("failed to terminate EC2 instance: %w", err)
	}

	p.logger.Info("EC2 instance termination initiated", "provider_instance_id", providerInstanceID)
	return nil
}

// GetInstanceStatus returns the current status of an EC2 instance
func (p *Provider) GetInstanceStatus(ctx context.Context, instanceID, providerInstanceID string) (*provider.InstanceStatus, error) {
	// Describe the instance directly by provider instance ID
	result, err := p.ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{providerInstanceID},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe EC2 instance: %w", err)
	}

	// Find the instance in the response
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			if aws.ToString(instance.InstanceId) == providerInstanceID {
				instanceID := p.extractInstanceIDFromTags(instance.Tags)
				return p.convertToInstanceStatus(instanceID, &instance), nil
			}
		}
	}

	return nil, fmt.Errorf("%w: %s", provider.ErrInstanceNotFound, providerInstanceID)
}

// ListInstances returns instances with pagination support for large result sets
func (p *Provider) ListInstances(ctx context.Context, req provider.ListInstancesRequest) (*provider.ListInstancesResponse, error) {
	p.logger.Debug("Listing EC2 instances",
		"cluster_id", req.ClusterID,
		"shard", req.Shard,
		"limit", req.Limit)

	// Set default limit
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.Limit > 1000 {
		req.Limit = 1000 // AWS API limit
	}

	// Build EC2 filters
	var ec2Filters []types.Filter

	// Add cluster and zone shard filters
	if req.ClusterID != "" {
		ec2Filters = append(ec2Filters, types.Filter{
			Name:   aws.String("tag:" + tagClusterID),
			Values: []string{req.ClusterID},
		})
	}
	if req.Shard != "" {
		ec2Filters = append(ec2Filters, types.Filter{
			Name:   aws.String("tag:" + tagShard),
			Values: []string{req.Shard},
		})
	}

	// Add filter for instances managed by Nstance
	ec2Filters = append(ec2Filters, types.Filter{
		Name:   aws.String("tag:" + tagNstanceManaged),
		Values: []string{"true"},
	})

	// Exclude terminated instances - they remain visible in AWS for ~1 hour after termination
	// but should not be considered as existing for reconciliation purposes
	ec2Filters = append(ec2Filters, types.Filter{
		Name: aws.String("instance-state-name"),
		Values: []string{
			string(types.InstanceStateNamePending),
			string(types.InstanceStateNameRunning),
			string(types.InstanceStateNameStopping),
			string(types.InstanceStateNameStopped),
			string(types.InstanceStateNameShuttingDown),
		},
	})

	// Prepare describe input
	describeInput := &ec2.DescribeInstancesInput{
		Filters:    ec2Filters,
		MaxResults: aws.Int32(int32(req.Limit)),
	}

	// Add pagination token if provided
	if req.NextToken != "" {
		describeInput.NextToken = aws.String(req.NextToken)
	}

	// Describe instances
	result, err := p.ec2Client.DescribeInstances(ctx, describeInput)
	if err != nil {
		return nil, fmt.Errorf("failed to list EC2 instances: %w", err)
	}

	// Convert to InstanceStatus
	var instances []*provider.InstanceStatus
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			// Extract Nstance instance ID from tags
			instanceID := p.extractInstanceIDFromTags(instance.Tags)
			// Include all instances that match our filters, even if they don't have an InstanceId tag
			// This is important for garbage collection to find dangling instances
			if instanceID == "" {
				// Generate a placeholder ID for tracking purposes
				instanceID = fmt.Sprintf("dangling-%s", aws.ToString(instance.InstanceId))
			}
			instances = append(instances, p.convertToInstanceStatus(instanceID, &instance))
		}
	}

	response := &provider.ListInstancesResponse{
		Instances: instances,
	}

	// Set next token if there are more results
	if result.NextToken != nil {
		response.NextToken = aws.ToString(result.NextToken)
	}

	p.logger.Debug("Listed EC2 instances with pagination",
		"count", len(instances),
		"has_next", response.NextToken != "")

	return response, nil
}

// Helper functions

// applyProviderArgs applies provider-specific arguments to RunInstances input
func (p *Provider) applyProviderArgs(input *ec2.RunInstancesInput, args map[string]interface{}) error {
	for key, val := range args {
		switch key {
		case "ImageId":
			if imageID, ok := val.(string); ok {
				input.ImageId = aws.String(imageID)
			} else {
				return fmt.Errorf("ImageId must be a string")
			}
		case "SecurityGroupIds":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal SecurityGroupIds: %w", err)
			} else {
				var sgIDs []string
				if err := json.Unmarshal(data, &sgIDs); err != nil {
					return fmt.Errorf("failed to unmarshal SecurityGroupIds: %w", err)
				}
				input.SecurityGroupIds = sgIDs
			}
		case "IamInstanceProfile":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal IamInstanceProfile: %w", err)
			} else {
				var profile types.IamInstanceProfileSpecification
				if err := json.Unmarshal(data, &profile); err != nil {
					return fmt.Errorf("failed to unmarshal IamInstanceProfile: %w", err)
				}
				input.IamInstanceProfile = &profile
			}
		case "BlockDeviceMappings":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal BlockDeviceMappings: %w", err)
			} else {
				var mappings []types.BlockDeviceMapping
				if err := json.Unmarshal(data, &mappings); err != nil {
					return fmt.Errorf("failed to unmarshal BlockDeviceMappings: %w", err)
				}
				input.BlockDeviceMappings = mappings
			}
		case "MetadataOptions":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal MetadataOptions: %w", err)
			} else {
				var opts types.InstanceMetadataOptionsRequest
				if err := json.Unmarshal(data, &opts); err != nil {
					return fmt.Errorf("failed to unmarshal MetadataOptions: %w", err)
				}
				input.MetadataOptions = &opts
			}
		case "Ipv6AddressCount":
			if i, ok := val.(float64); ok {
				input.Ipv6AddressCount = aws.Int32(int32(i))
			} else {
				return fmt.Errorf("Ipv6AddressCount must be a number")
			}
		case "PrivateDnsNameOptions":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal PrivateDnsNameOptions: %w", err)
			} else {
				var opts types.PrivateDnsNameOptionsRequest
				if err := json.Unmarshal(data, &opts); err != nil {
					return fmt.Errorf("failed to unmarshal PrivateDnsNameOptions: %w", err)
				}
				input.PrivateDnsNameOptions = &opts
			}
		case "TagSpecifications":
			if data, err := json.Marshal(val); err != nil {
				return fmt.Errorf("failed to marshal TagSpecifications: %w", err)
			} else {
				var specs []types.TagSpecification
				if err := json.Unmarshal(data, &specs); err != nil {
					return fmt.Errorf("failed to unmarshal TagSpecifications: %w", err)
				}
				for _, spec := range specs {
					if spec.ResourceType == types.ResourceTypeInstance {
						if len(input.TagSpecifications) > 0 {
							existingKeys := make(map[string]bool)
							for _, t := range input.TagSpecifications[0].Tags {
								existingKeys[aws.ToString(t.Key)] = true
							}
							for _, t := range spec.Tags {
								tagKey := aws.ToString(t.Key)
								if tagKey == "Name" && existingKeys["Name"] {
									p.logger.Debug("Overriding default Name tag from config", "name", aws.ToString(t.Value))
									filteredTags := make([]types.Tag, 0, len(input.TagSpecifications[0].Tags))
									for _, existing := range input.TagSpecifications[0].Tags {
										if aws.ToString(existing.Key) != "Name" {
											filteredTags = append(filteredTags, existing)
										}
									}
									input.TagSpecifications[0].Tags = append(filteredTags, t)
									existingKeys["Name"] = true
								} else if !existingKeys[tagKey] {
									input.TagSpecifications[0].Tags = append(input.TagSpecifications[0].Tags, t)
								} else {
									p.logger.Debug("Skipping duplicate tag key from TagSpecifications", "key", tagKey)
								}
							}
						} else {
							input.TagSpecifications = append(input.TagSpecifications, spec)
						}
					} else {
						input.TagSpecifications = append(input.TagSpecifications, spec)
					}
				}
			}
		default:
			p.logger.Warn("Unknown provider argument", "key", key, "value", val)
		}
	}
	return nil
}

// extractInstanceIDFromTags extracts the Nstance instance ID from EC2 tags
func (p *Provider) extractInstanceIDFromTags(tags []types.Tag) string {
	for _, tag := range tags {
		if aws.ToString(tag.Key) == tagInstanceID {
			return aws.ToString(tag.Value)
		}
	}
	return ""
}

// convertToInstanceStatus converts an EC2 instance to InstanceStatus
func (p *Provider) convertToInstanceStatus(instanceID string, instance *types.Instance) *provider.InstanceStatus {
	status := &provider.InstanceStatus{
		InstanceID:         instanceID,
		ProviderInstanceID: aws.ToString(instance.InstanceId),
		Status:             p.convertInstanceState(instance.State),
		InstanceType:       string(instance.InstanceType),
		Region:             p.config.Region,
		Zone:               aws.ToString(instance.Placement.AvailabilityZone),
		LaunchedAt:         aws.ToTime(instance.LaunchTime),
	}

	// Add IP addresses
	if instance.PrivateIpAddress != nil {
		status.PrivateIPv4 = aws.ToString(instance.PrivateIpAddress)
	}
	if instance.Ipv6Address != nil {
		status.PrivateIPv6 = aws.ToString(instance.Ipv6Address)
	}

	// Extract hostname from private DNS name (first segment before the first dot)
	if instance.PrivateDnsName != nil {
		privateDnsName := aws.ToString(instance.PrivateDnsName)
		if privateDnsName != "" {
			status.Hostname = strings.Split(privateDnsName, ".")[0]
		}
	}

	// Extract tags
	status.Tags = make(map[string]string)
	var group, kind string
	for _, tag := range instance.Tags {
		key := aws.ToString(tag.Key)
		value := aws.ToString(tag.Value)
		status.Tags[key] = value
		switch key {
		case tagClusterID:
			status.ClusterID = value
		case tagShard:
			status.Shard = value
		case tagGroup:
			group = value
		case tagInstanceKind:
			kind = value
		}
	}

	// Populate annotations if available
	if group != "" || kind != "" {
		status.Annotations = &provider.InstanceAnnotations{
			Group: group,
			Kind:  kind,
		}
	}

	return status
}

// convertInstanceState converts EC2 instance state to standard status
func (p *Provider) convertInstanceState(state *types.InstanceState) string {
	if state == nil {
		return provider.StatusUnknown
	}

	switch state.Name {
	case types.InstanceStateNamePending:
		return provider.StatusPending
	case types.InstanceStateNameRunning:
		return provider.StatusRunning
	case types.InstanceStateNameStopping:
		return provider.StatusStopping
	case types.InstanceStateNameStopped:
		return provider.StatusStopped
	case types.InstanceStateNameShuttingDown:
		return provider.StatusDeleting
	case types.InstanceStateNameTerminated:
		return provider.StatusDeleted
	default:
		return provider.StatusUnknown
	}
}

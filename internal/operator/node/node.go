// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package node

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FindByProviderID finds a node by matching the provider instance ID within the providerID field.
func FindByProviderID(ctx context.Context, c client.Client, providerInstanceID string) (*corev1.Node, error) {
	nodeList := &corev1.NodeList{}
	if err := c.List(ctx, nodeList); err != nil {
		return nil, err
	}

	for _, n := range nodeList.Items {
		if n.Spec.ProviderID != "" && MatchesProviderID(n.Spec.ProviderID, providerInstanceID) {
			return &n, nil
		}
	}

	return nil, nil
}

// MatchesProviderID checks if the provider instance ID matches the node's providerID.
// Handles cloud-specific providerID formats:
//   - AWS: aws:///us-west-2a/i-1234567890abcdef0
//   - Google: gce://project/zone/instance-name
func MatchesProviderID(nodeProviderID, providerInstanceID string) bool {
	if nodeProviderID == "" || providerInstanceID == "" {
		return false
	}

	// Direct match
	if nodeProviderID == providerInstanceID {
		return true
	}

	// Extract instance ID from cloud-specific formats
	// AWS format: aws:///zone/instance-id or aws://account-id/zone/instance-id
	if strings.HasPrefix(nodeProviderID, "aws://") {
		parts := strings.Split(nodeProviderID, "/")
		if len(parts) > 0 {
			instanceID := parts[len(parts)-1]
			if instanceID == providerInstanceID {
				return true
			}
		}
	}

	// Google format: gce://project/zone/instance-name
	if strings.HasPrefix(nodeProviderID, "gce://") {
		parts := strings.Split(nodeProviderID, "/")
		if len(parts) > 0 {
			instanceID := parts[len(parts)-1]
			if instanceID == providerInstanceID {
				return true
			}
		}
	}

	return false
}

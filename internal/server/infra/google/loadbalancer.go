// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"google.golang.org/api/compute/v1"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// RegisterWithLB attaches an instance IP endpoint to its configured zonal NEG.
func (p *Provider) RegisterWithLB(ctx context.Context, req provider.RegisterLBRequest) error {
	neg, endpoint, err := p.resolveNetworkEndpoint(ctx, req)
	if err != nil {
		return err
	}
	op, err := p.computeService.NetworkEndpointGroups.AttachNetworkEndpoints(
		p.options.ProjectID, req.Zone, neg,
		&compute.NetworkEndpointGroupsAttachEndpointsRequest{NetworkEndpoints: []*compute.NetworkEndpoint{endpoint}},
	).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("attaching instance %s to NEG %s: %w", req.ProviderInstanceID, neg, err)
	}
	return p.waitZoneOperation(ctx, req.Zone, op)
}

// DeregisterFromLB detaches an instance IP endpoint from its configured zonal NEG.
func (p *Provider) DeregisterFromLB(ctx context.Context, req provider.DeregisterLBRequest) error {
	registerReq := provider.RegisterLBRequest(req)
	neg, endpoint, err := p.resolveNetworkEndpoint(ctx, registerReq)
	if err != nil {
		return err
	}
	op, err := p.computeService.NetworkEndpointGroups.DetachNetworkEndpoints(
		p.options.ProjectID, req.Zone, neg,
		&compute.NetworkEndpointGroupsDetachEndpointsRequest{NetworkEndpoints: []*compute.NetworkEndpoint{endpoint}},
	).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("detaching instance %s from NEG %s: %w", req.ProviderInstanceID, neg, err)
	}
	return p.waitZoneOperation(ctx, req.Zone, op)
}

// GetLBTargetState returns an endpoint's aggregate Google frontend health state.
func (p *Provider) GetLBTargetState(ctx context.Context, req provider.RegisterLBRequest) (provider.LBTargetState, error) {
	neg, endpoint, err := p.resolveNetworkEndpoint(ctx, req)
	if err != nil {
		if isNotFound(err) {
			return provider.LBTargetDeregistered, nil
		}
		return "", err
	}
	expectedBackends, err := p.resolveFrontendBackends(ctx, req.LBConfig)
	if err != nil {
		return "", err
	}
	state := provider.LBTargetDeregistered
	err = p.computeService.NetworkEndpointGroups.ListNetworkEndpoints(
		p.options.ProjectID, req.Zone, neg,
		&compute.NetworkEndpointGroupsListEndpointsRequest{HealthStatus: "SHOW"},
	).Pages(ctx, func(result *compute.NetworkEndpointGroupsListNetworkEndpoints) error {
		for _, item := range result.Items {
			if sameNetworkEndpoint(item.NetworkEndpoint, endpoint) {
				state = networkEndpointHealthState(item, expectedBackends)
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("listing endpoints in NEG %s: %w", neg, err)
	}
	return state, nil
}

// ListLBInstances lists the union of instances attached to the configured zonal NEGs.
func (p *Provider) ListLBInstances(ctx context.Context, req provider.ListLBInstancesRequest) ([]string, error) {
	if _, err := p.resolveFrontendBackends(ctx, req.LBConfig); err != nil {
		return nil, err
	}
	negs, err := p.inspectNetworkEndpointGroups(ctx, req.Zone, req.LBConfig.NetworkEndpointGroups)
	if err != nil {
		return nil, err
	}
	instances := make(map[string]struct{})
	for _, neg := range negs {
		err := p.computeService.NetworkEndpointGroups.ListNetworkEndpoints(
			p.options.ProjectID, req.Zone, neg.Name,
			&compute.NetworkEndpointGroupsListEndpointsRequest{HealthStatus: "SKIP"},
		).Pages(ctx, func(result *compute.NetworkEndpointGroupsListNetworkEndpoints) error {
			for _, item := range result.Items {
				if item.NetworkEndpoint != nil && item.NetworkEndpoint.Instance != "" {
					instances[path.Base(item.NetworkEndpoint.Instance)] = struct{}{}
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("listing endpoints in NEG %s: %w", neg.Name, err)
		}
	}
	result := make([]string, 0, len(instances))
	for instance := range instances {
		result = append(result, instance)
	}
	return result, nil
}

// resolveNetworkEndpoint finds the configured NEG and endpoint matching an instance interface.
func (p *Provider) resolveNetworkEndpoint(ctx context.Context, req provider.RegisterLBRequest) (string, *compute.NetworkEndpoint, error) {
	if req.Zone == "" {
		return "", nil, fmt.Errorf("zone is required for Google Cloud NEG membership")
	}
	instance, err := p.computeService.Instances.Get(p.options.ProjectID, req.Zone, req.ProviderInstanceID).Context(ctx).Do()
	if err != nil {
		return "", nil, fmt.Errorf("getting instance %s: %w", req.ProviderInstanceID, err)
	}
	negs, err := p.inspectNetworkEndpointGroups(ctx, req.Zone, req.LBConfig.NetworkEndpointGroups)
	if err != nil {
		return "", nil, err
	}
	for _, nic := range instance.NetworkInterfaces {
		for _, neg := range negs {
			if resourceKey(neg.Subnetwork) == resourceKey(nic.Subnetwork) {
				return neg.Name, &compute.NetworkEndpoint{Instance: req.ProviderInstanceID, IpAddress: nic.NetworkIP}, nil
			}
		}
	}
	return "", nil, fmt.Errorf("no configured NEG matches an interface subnet on instance %s", req.ProviderInstanceID)
}

// inspectNetworkEndpointGroups validates and returns the configured zonal NEGs.
func (p *Provider) inspectNetworkEndpointGroups(ctx context.Context, zone string, names []string) ([]*compute.NetworkEndpointGroup, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one network endpoint group is required")
	}
	seenSubnets := make(map[string]string)
	negs := make([]*compute.NetworkEndpointGroup, 0, len(names))
	for _, name := range names {
		neg, err := p.computeService.NetworkEndpointGroups.Get(p.options.ProjectID, zone, name).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("getting NEG %s: %w", name, err)
		}
		if neg.NetworkEndpointType != "GCE_VM_IP" {
			return nil, fmt.Errorf("NEG %s has type %s, want GCE_VM_IP", name, neg.NetworkEndpointType)
		}
		if path.Base(neg.Zone) != zone {
			return nil, fmt.Errorf("NEG %s is in zone %s, want %s", name, path.Base(neg.Zone), zone)
		}
		subnet := resourceKey(neg.Subnetwork)
		if previous := seenSubnets[subnet]; previous != "" {
			return nil, fmt.Errorf("NEGs %s and %s map to the same subnet", previous, name)
		}
		seenSubnets[subnet] = name
		negs = append(negs, neg)
	}
	return negs, nil
}

// waitZoneOperation waits for a Google Cloud zonal operation to complete.
func (p *Provider) waitZoneOperation(ctx context.Context, zone string, operation *compute.Operation) error {
	for operation.Status != "DONE" {
		operationName := operation.Name
		updated, err := p.computeService.ZoneOperations.Wait(p.options.ProjectID, zone, operationName).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("waiting for zone operation %s: %w", operationName, err)
		}
		operation = updated
	}
	if operation.Error != nil || operation.HttpErrorStatusCode != 0 {
		return fmt.Errorf("zone operation %s failed: %s", operation.Name, operation.HttpErrorMessage)
	}
	return nil
}

// sameNetworkEndpoint reports whether two endpoints identify the same instance address.
func sameNetworkEndpoint(left, right *compute.NetworkEndpoint) bool {
	return left != nil && right != nil && path.Base(left.Instance) == path.Base(right.Instance) && left.IpAddress == right.IpAddress
}

// networkEndpointHealthState combines health across the expected frontend/backend pairs.
func networkEndpointHealthState(item *compute.NetworkEndpointWithHealthStatus, expectedPairs map[string]struct{}) provider.LBTargetState {
	if len(item.Healths) == 0 {
		return provider.LBTargetRegistered
	}
	healthy := make(map[string]struct{}, len(expectedPairs))
	for _, health := range item.Healths {
		if health.BackendService == nil || health.ForwardingRule == nil {
			continue
		}
		pair := frontendBackendPair(health.ForwardingRule.ForwardingRule, health.BackendService.BackendService)
		if _, expected := expectedPairs[pair]; !expected {
			continue
		}
		switch health.HealthState {
		case "DRAINING":
			return provider.LBTargetDraining
		case "HEALTHY":
			healthy[pair] = struct{}{}
		}
	}
	if len(healthy) == len(expectedPairs) {
		return provider.LBTargetHealthy
	}
	return provider.LBTargetRegistered
}

// resolveFrontendBackends maps every configured frontend to its backend service.
func (p *Provider) resolveFrontendBackends(ctx context.Context, config provider.LoadBalancerConfig) (map[string]struct{}, error) {
	if len(config.Frontends) == 0 {
		return nil, fmt.Errorf("at least one Google frontend is required")
	}
	matched := make(map[string]*compute.ForwardingRule, len(config.Frontends))
	err := p.computeService.ForwardingRules.List(p.options.ProjectID, p.config.Region).Pages(ctx, func(page *compute.ForwardingRuleList) error {
		for _, rule := range page.Items {
			for _, frontend := range config.Frontends {
				key := fmt.Sprintf("%s:%d", frontend.IP, frontend.Port)
				if rule.IPAddress == frontend.IP && rule.IPProtocol == "TCP" && forwardingRuleIncludesPort(rule, frontend.Port) {
					if matched[key] != nil {
						return fmt.Errorf("multiple forwarding rules match Google frontend %s", key)
					}
					matched[key] = rule
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("resolving Google forwarding rules: %w", err)
	}
	pairs := make(map[string]struct{}, len(config.Frontends))
	for _, frontend := range config.Frontends {
		key := fmt.Sprintf("%s:%d", frontend.IP, frontend.Port)
		rule := matched[key]
		if rule == nil || rule.BackendService == "" {
			return nil, fmt.Errorf("no forwarding rule matches Google frontend %s", key)
		}
		pairs[frontendBackendPair(rule.SelfLink, rule.BackendService)] = struct{}{}
	}
	return pairs, nil
}

// frontendBackendPair returns the stable identity used to correlate health results.
func frontendBackendPair(forwardingRule, backend string) string {
	return resourceKey(forwardingRule) + "|" + resourceKey(backend)
}

// forwardingRuleIncludesPort reports whether a forwarding rule serves a port.
func forwardingRuleIncludesPort(rule *compute.ForwardingRule, port int) bool {
	if rule.AllPorts {
		return true
	}
	wanted := strconv.Itoa(port)
	for _, configured := range rule.Ports {
		if configured == wanted {
			return true
		}
	}
	parts := strings.Split(rule.PortRange, "-")
	if len(parts) == 1 {
		return parts[0] == wanted
	}
	if len(parts) == 2 {
		first, firstErr := strconv.Atoi(parts[0])
		last, lastErr := strconv.Atoi(parts[1])
		return firstErr == nil && lastErr == nil && port >= first && port <= last
	}
	return false
}

// resourceKey removes inconsequential trailing separators from resource URLs.
func resourceKey(value string) string { return strings.TrimSuffix(value, "/") }

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"testing"

	"google.golang.org/api/compute/v1"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// TestNetworkEndpointHealthStateRequiresEveryExpectedBackend verifies aggregate health.
func TestNetworkEndpointHealthStateRequiresEveryExpectedBackend(t *testing.T) {
	item := &compute.NetworkEndpointWithHealthStatus{Healths: []*compute.HealthStatusForNetworkEndpoint{
		{ForwardingRule: &compute.ForwardingRuleReference{ForwardingRule: "regions/us/forwardingRules/unrelated"}, BackendService: &compute.BackendServiceReference{BackendService: "regions/us/backendServices/unrelated"}, HealthState: "HEALTHY"},
		{ForwardingRule: &compute.ForwardingRuleReference{ForwardingRule: "regions/us/forwardingRules/api"}, BackendService: &compute.BackendServiceReference{BackendService: "regions/us/backendServices/shared"}, HealthState: "HEALTHY"},
	}}
	expected := map[string]struct{}{
		frontendBackendPair("regions/us/forwardingRules/api", "regions/us/backendServices/shared"):     {},
		frontendBackendPair("regions/us/forwardingRules/ingress", "regions/us/backendServices/shared"): {},
	}
	if state := networkEndpointHealthState(item, expected); state != provider.LBTargetRegistered {
		t.Fatalf("state = %s, want registered while an expected backend is missing", state)
	}
	item.Healths = append(item.Healths, &compute.HealthStatusForNetworkEndpoint{
		ForwardingRule: &compute.ForwardingRuleReference{ForwardingRule: "regions/us/forwardingRules/ingress"},
		BackendService: &compute.BackendServiceReference{BackendService: "regions/us/backendServices/shared"},
		HealthState:    "HEALTHY",
	})
	if state := networkEndpointHealthState(item, expected); state != provider.LBTargetHealthy {
		t.Fatalf("state = %s, want healthy", state)
	}
	item.Healths[2].HealthState = "DRAINING"
	if state := networkEndpointHealthState(item, expected); state != provider.LBTargetDraining {
		t.Fatalf("state = %s, want draining", state)
	}
}

// TestForwardingRuleIncludesPort verifies explicit, ranged, and all-port rules.
func TestForwardingRuleIncludesPort(t *testing.T) {
	for name, rule := range map[string]*compute.ForwardingRule{
		"all":   {AllPorts: true},
		"ports": {Ports: []string{"443"}},
		"range": {PortRange: "400-500"},
	} {
		if !forwardingRuleIncludesPort(rule, 443) {
			t.Errorf("%s rule did not include port 443", name)
		}
	}
	if forwardingRuleIncludesPort(&compute.ForwardingRule{PortRange: "80-90"}, 443) {
		t.Fatal("unrelated range included port 443")
	}
}

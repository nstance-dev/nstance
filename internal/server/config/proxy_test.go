// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxyConfigDerivation verifies provider resources do not enter static proxy config.
func TestProxyConfigDerivation(t *testing.T) {
	cfg := newTestConfig()
	cfg.LoadBalancers = map[string]LoadBalancerConfig{
		"api": {Provider: "aws", TargetGroups: []AWSTargetGroupConfig{{ARN: "arn", ListenerPort: 6443, TargetPort: 6443, ProxyPort: 16443}}},
	}
	cfg.Groups = map[string]map[string]GroupConfig{
		"default": {
			"workers": {Template: "knd", LoadBalancers: []string{"api"}},
			"control": {Template: "knc", LoadBalancers: []string{"api"}},
		},
	}
	got, err := cfg.ProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	listener := got.Listeners["api:16443"]
	if listener.Tenant != "default" || listener.TargetPort != 6443 || listener.ProxyPort != 16443 || strings.Join(listener.Groups, ",") != "control,workers" {
		t.Fatalf("unexpected listener: %#v", listener)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "arn") || strings.Contains(string(data), "provider") || strings.Contains(string(data), "destination_ip") {
		t.Fatalf("static config leaked provider data or optional destination: %s", data)
	}
}

// TestProxyConfigDistinguishesGoogleFrontends verifies destination IPs identify
// listeners that share a port.
func TestProxyConfigDistinguishesGoogleFrontends(t *testing.T) {
	cfg := newTestConfig()
	cfg.LoadBalancers = map[string]LoadBalancerConfig{
		"api": {
			Provider: "google",
			Frontends: []GoogleNLBFrontendConfig{
				{IP: "34.0.0.1", Port: 16443},
				{IP: "34.0.0.2", Port: 16443},
			},
		},
	}
	cfg.Groups = map[string]map[string]GroupConfig{
		"default": {"control": {Template: "knc", LoadBalancers: []string{"api"}}},
	}

	got, err := cfg.ProxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Listeners) != 2 {
		t.Fatalf("listeners = %#v", got.Listeners)
	}
	for _, ip := range []string{"34.0.0.1", "34.0.0.2"} {
		key := "api/" + ip + ":16443"
		if got.Listeners[key].DestinationIP != ip {
			t.Fatalf("listener %q = %#v", key, got.Listeners[key])
		}
	}
}

// TestProxyConfigRejectsInvalidReferencesAndCollisions covers invalid listener topology.
func TestProxyConfigRejectsInvalidReferencesAndCollisions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"unreferenced", func(c *Config) { c.Groups = nil }, "not referenced"},
		{"cross tenant", func(c *Config) {
			c.Groups["other"] = map[string]GroupConfig{"group": {Template: "knc", LoadBalancers: []string{"api"}}}
		}, "across tenants"},
		{"server port", func(c *Config) {
			c.LoadBalancers["api"] = LoadBalancerConfig{Provider: "tunnel", Listeners: []TunnelListenerConfig{{TargetPort: 443, ProxyPort: 8994}}}
		}, "nstance-server port"},
		{"proxy port", func(c *Config) {
			c.LoadBalancers["other"] = LoadBalancerConfig{Provider: "tunnel", Listeners: []TunnelListenerConfig{{TargetPort: 443, ProxyPort: 16443}}}
			g := c.Groups["default"]["control"]
			g.LoadBalancers = append(g.LoadBalancers, "other")
			c.Groups["default"]["control"] = g
		}, "proxy port"},
		{"duplicate Google selector", func(c *Config) {
			c.LoadBalancers["api"] = LoadBalancerConfig{Provider: "google", NetworkEndpointGroups: []string{"api-a"}, Frontends: []GoogleNLBFrontendConfig{{IP: "34.0.0.1", Port: 16443}}}
			c.LoadBalancers["other"] = LoadBalancerConfig{Provider: "google", NetworkEndpointGroups: []string{"api-b"}, Frontends: []GoogleNLBFrontendConfig{{IP: "34.0.0.1", Port: 16443}}}
			g := c.Groups["default"]["control"]
			g.LoadBalancers = append(g.LoadBalancers, "other")
			c.Groups["default"]["control"] = g
		}, "selector"},
		{"Google and exclusive port", func(c *Config) {
			c.LoadBalancers["api"] = LoadBalancerConfig{Provider: "google", NetworkEndpointGroups: []string{"api-a"}, Frontends: []GoogleNLBFrontendConfig{{IP: "34.0.0.1", Port: 16443}}}
			c.LoadBalancers["other"] = LoadBalancerConfig{Provider: "tunnel", Listeners: []TunnelListenerConfig{{TargetPort: 443, ProxyPort: 16443}}}
			g := c.Groups["default"]["control"]
			g.LoadBalancers = append(g.LoadBalancers, "other")
			c.Groups["default"]["control"] = g
		}, "proxy port"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newTestConfig()
			cfg.LoadBalancers = map[string]LoadBalancerConfig{"api": {Provider: "tunnel", Listeners: []TunnelListenerConfig{{TargetPort: 6443, ProxyPort: 16443}}}}
			cfg.Groups = map[string]map[string]GroupConfig{"default": {"control": {Template: "knc", LoadBalancers: []string{"api"}}}}
			tt.mutate(cfg)
			_, err := cfg.ProxyConfig()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

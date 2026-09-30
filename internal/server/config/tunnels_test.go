// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
)

// TestTunnelValidation verifies tunnel static-Pod security boundaries.
func TestTunnelValidation(t *testing.T) {
	digest := "example.invalid/tunnel@sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name: "dedicated",
			config: Config{
				LoadBalancers: map[string]LoadBalancerConfig{"api": {Provider: "tunnel"}},
				Tunnels:       map[string]TunnelPodConfig{"api": {Image: digest, ReadinessURL: "http://127.0.0.1:2000/ready"}},
			},
		},
		{
			name: "mutable image",
			config: Config{
				LoadBalancers: map[string]LoadBalancerConfig{"api": {Provider: "tunnel"}},
				Tunnels:       map[string]TunnelPodConfig{"api": {Image: "example.invalid/tunnel:latest", ReadinessURL: "http://127.0.0.1:2000/ready"}},
			},
			wantErr: "immutable sha256 digest",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.validateTunnels()
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

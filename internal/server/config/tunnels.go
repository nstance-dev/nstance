// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"

	"github.com/nstance-dev/nstance/internal/identifiers"
)

// validateTunnels validates provider-neutral tunnel runtimes and references.
func (c *Config) validateTunnels() error {
	for name, tunnel := range c.Tunnels {
		if err := identifiers.Validate("tunnel", name); err != nil {
			return err
		}
		if err := validateTunnelPod("tunnel "+name, tunnel); err != nil {
			return err
		}
	}
	for name, loadBalancer := range c.LoadBalancers {
		if loadBalancer.Provider != "tunnel" {
			continue
		}
		if _, ok := c.Tunnels[name]; !ok {
			return fmt.Errorf("tunnel load balancer %s requires a matching tunnels entry", name)
		}
	}
	for name := range c.Tunnels {
		loadBalancer, ok := c.LoadBalancers[name]
		if !ok || loadBalancer.Provider != "tunnel" {
			return fmt.Errorf("tunnel %s requires a matching tunnel load balancer", name)
		}
	}
	return nil
}

// validateTunnelPod validates one immutable hardened static-Pod definition.
func validateTunnelPod(owner string, pod TunnelPodConfig) error {
	parts := strings.Split(pod.Image, "@sha256:")
	if len(parts) != 2 || parts[0] == "" || len(parts[1]) != 64 {
		return fmt.Errorf("%s image must use an immutable sha256 digest", owner)
	}
	for _, char := range parts[1] {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return fmt.Errorf("%s image must use a lowercase sha256 digest", owner)
		}
	}
	if err := validateLoopbackURL(owner+" readiness_url", pod.ReadinessURL); err != nil {
		return err
	}
	if pod.ReadinessTimeout < 0 {
		return fmt.Errorf("%s readiness_timeout must not be negative", owner)
	}
	for destination, file := range pod.Files {
		if !strings.HasPrefix(destination, "/") || path.Clean(destination) != destination || destination == "/" {
			return fmt.Errorf("%s file destination %q must be a clean absolute file path", owner, destination)
		}
		if file.Kind != "secret" && file.Kind != "storage" {
			return fmt.Errorf("%s file %s must use kind secret or storage", owner, destination)
		}
		if file.Source == "" {
			return fmt.Errorf("%s file %s requires a source", owner, destination)
		}
	}
	return nil
}

// validateLoopbackURL requires a plain HTTP URL bound to the local host.
func validateLoopbackURL(owner, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.Port() == "" {
		return fmt.Errorf("%s must be a loopback HTTP URL", owner)
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("%s must be a loopback HTTP URL", owner)
	}
	return nil
}

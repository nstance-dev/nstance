// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// WaitForIPBindable waits for the specified IP address to be bindable at the OS level.
// Cloud provider APIs may report network interface attachment or IP aliasing as complete
// before the IP is actually usable on the host. This function polls by attempting a
// test net.Listen on the IP with an ephemeral port (port 0), closing the listener
// immediately on success. This approach works regardless of whether the IP was added via
// interface assignment (e.g. AWS ENI attached via DHCP) or local route (e.g. GCP guest
// agent's "ip route add to local"), both of which make the IP bindable without necessarily
// adding it to net.InterfaceAddrs.
func WaitForIPBindable(ctx context.Context, logger *slog.Logger, ipAddr string) error {
	logger.Info("Waiting for IP to be available at OS level", "ipAddress", ipAddr)

	if net.ParseIP(ipAddr) == nil {
		return fmt.Errorf("invalid IP address: %s", ipAddr)
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.After(30 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for IP %s to become available at OS level", ipAddr)
		case <-ticker.C:
			ln, err := net.Listen("tcp", net.JoinHostPort(ipAddr, "0"))
			if err == nil {
				_ = ln.Close()
				logger.Info("IP is now available at OS level", "ipAddress", ipAddr)
				return nil
			}
		}
	}
}

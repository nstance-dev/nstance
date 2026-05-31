// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"net"
)

// isIPv4 returns true if the host string represents an IPv4 address.
// Returns false for IPv6 addresses or invalid/unparseable hosts.
func isIPv4(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.To4() != nil
}

// detectPrimaryIP returns the IP address of the primary network interface.
// It only returns addresses matching the IP version of bindHost.
func detectPrimaryIP(bindHost string) (string, error) {
	wantIPv4 := isIPv4(bindHost)

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("failed to get interface addresses: %w", err)
	}

	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}

		isV4 := ipnet.IP.To4() != nil
		if isV4 == wantIPv4 {
			return ipnet.IP.String(), nil
		}
	}

	if wantIPv4 {
		return "", fmt.Errorf("no suitable IPv4 address found")
	}
	return "", fmt.Errorf("no suitable IPv6 address found")
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package identity

import "net"

// LocalIPs returns the private IPv4 and IPv6 addresses for this host.
// If overrideV4 or overrideV6 are provided, those values are used.
// Otherwise, auto-detects the first non-loopback addresses found on the system.
// Returns empty strings if no suitable addresses are found.
func LocalIPs(overrideV4, overrideV6 string) (ipv4, ipv6 string) {
	if overrideV4 != "" && overrideV6 != "" {
		return overrideV4, overrideV6
	}

	detectedV4, detectedV6 := detectLocalIPs()

	ipv4 = overrideV4
	if ipv4 == "" {
		ipv4 = detectedV4
	}

	ipv6 = overrideV6
	if ipv6 == "" {
		ipv6 = detectedV6
	}

	return ipv4, ipv6
}

// detectLocalIPs returns the first non-loopback private IPv4 and IPv6 addresses found on the system.
func detectLocalIPs() (ipv4, ipv6 string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", ""
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() {
				continue
			}

			if ip4 := ip.To4(); ip4 != nil {
				if ipv4 == "" {
					ipv4 = ip4.String()
				}
			} else if ip.To16() != nil {
				if ipv6 == "" {
					ipv6 = ip.String()
				}
			}

			if ipv4 != "" && ipv6 != "" {
				return ipv4, ipv6
			}
		}
	}

	return ipv4, ipv6
}

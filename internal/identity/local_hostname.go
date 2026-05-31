// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package identity

import "os"

// LocalHostname returns the hostname for this host.
// If override is provided, that value is used.
// Otherwise, auto-detects the hostname using os.Hostname().
// Returns empty string if detection fails.
func LocalHostname(override string) string {
	if override != "" {
		return override
	}

	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	return hostname
}

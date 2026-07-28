// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxy

// Config is the static nstance-proxy configuration.
type Config struct {
	Listeners map[string]Listener `json:"listeners"`
}

// Listener identifies one wake-capable listener and its upstream groups.
type Listener struct {
	Tenant        string   `json:"tenant"`
	Groups        []string `json:"groups"`
	TargetPort    int      `json:"target_port"`
	ProxyPort     int      `json:"proxy_port"`
	DestinationIP string   `json:"destination_ip,omitempty"`
}

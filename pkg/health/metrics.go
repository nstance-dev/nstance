// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package health

// Metrics contains observations collected for one health report.
type Metrics struct {
	CPUUsage              *float64          `json:"cpu_usage,omitempty"`
	CPUCoreUsage          []float64         `json:"cpu_core_usage,omitempty"`
	MemoryUsage           *float64          `json:"memory_usage,omitempty"`
	NetworkInterface      *InterfaceMetrics `json:"network_interface,omitempty"`
	NetworkInterfaceError *string           `json:"network_interface_error,omitempty"`
	EBPFCounters          map[uint32]uint64 `json:"ebpf_counters,omitempty"`
	EBPFError             *string           `json:"ebpf_error,omitempty"`
	ConntrackCount        *uint64           `json:"conntrack_count,omitempty"`
	ConntrackMax          *uint64           `json:"conntrack_max,omitempty"`
	ConntrackError        *string           `json:"conntrack_error,omitempty"`
}

// InterfaceMetrics contains cumulative counters for one configured network interface.
type InterfaceMetrics struct {
	InterfaceName string `json:"interface_name"`
	RXBytes       uint64 `json:"rx_bytes"`
	TXBytes       uint64 `json:"tx_bytes"`
	RXPackets     uint64 `json:"rx_packets"`
	TXPackets     uint64 `json:"tx_packets"`
	RXDrops       uint64 `json:"rx_drops"`
	TXDrops       uint64 `json:"tx_drops"`
}

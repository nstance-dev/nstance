// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package health

// Metrics holds metrics averages for a period of time e.g. last 1 minute
type Metrics struct {
	// CPU Usage (percentage)
	CPUUsage     float64   `json:"cpu_usage"`
	CPUCoreUsage []float64 `json:"cpu_core_usage"`
	// Memory Usage (percentage)
	MemoryUsage float64 `json:"memory_usage"`
	// Network I/O (bytes)
	NetworkBytesSent     float64 `json:"network_bytes_sent"`
	NetworkBytesReceived float64 `json:"network_bytes_received"`
	// Disk Usage (percentage)
	DiskUsed       float64 `json:"disk_used"`
	DiskInodesUsed float64 `json:"disk_inodes_used"`
	// Disk I/O (bytes)
	DiskBytesRead    float64 `json:"disk_bytes_read"`
	DiskBytesWritten float64 `json:"disk_bytes_written"`
}

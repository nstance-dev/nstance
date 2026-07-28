// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// collectMetrics observes current CPU, memory, and configured interface metrics.
func collectMetrics(interfaceName string) Metrics {
	metrics := Metrics{}
	if coreUsage, err := cpu.Percent(100*time.Millisecond, true); err == nil && len(coreUsage) > 0 {
		metrics.CPUCoreUsage = coreUsage
		usage := mean(coreUsage)
		metrics.CPUUsage = &usage
	}
	if memory, err := mem.VirtualMemory(); err == nil {
		metrics.MemoryUsage = &memory.UsedPercent
	}
	interfaceName = strings.TrimSpace(interfaceName)
	if interfaceName != "" {
		collectInterface(interfaceName, &metrics)
		collectConntrack(&metrics)
	}
	return metrics
}

// collectInterface adds cumulative counters for the configured interface.
func collectInterface(interfaceName string, metrics *Metrics) {
	all, err := net.IOCounters(true)
	if err != nil {
		message := err.Error()
		metrics.NetworkInterfaceError = &message
		return
	}
	var current *net.IOCountersStat
	for i := range all {
		if all[i].Name == interfaceName {
			current = &all[i]
			break
		}
	}
	if current == nil {
		message := fmt.Sprintf("network interface %q not found", interfaceName)
		metrics.NetworkInterfaceError = &message
		return
	}

	metrics.NetworkInterface = &InterfaceMetrics{
		InterfaceName: interfaceName,
		RXBytes:       current.BytesRecv,
		TXBytes:       current.BytesSent,
		RXPackets:     current.PacketsRecv,
		TXPackets:     current.PacketsSent,
		RXDrops:       current.Dropin,
		TXDrops:       current.Dropout,
	}
}

// collectConntrack reads the current conntrack occupancy and configured limit.
func collectConntrack(metrics *Metrics) {
	count, countErr := readUint("/proc/sys/net/netfilter/nf_conntrack_count")
	maximum, maxErr := readUint("/proc/sys/net/netfilter/nf_conntrack_max")
	if countErr != nil || maxErr != nil {
		message := fmt.Sprintf("read conntrack metrics: count: %v; max: %v", countErr, maxErr)
		metrics.ConntrackError = &message
		return
	}
	metrics.ConntrackCount = &count
	metrics.ConntrackMax = &maximum
}

// mean returns the arithmetic mean of values.
func mean(values []float64) float64 {
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

// readUint reads one base-10 unsigned integer from a file.
func readUint(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"fmt"
	"math"
	"time"

	"log/slog"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// MAX_WINDOW_SECONDS is the maximum number of samples to keep in memory (15
// minutes, as used by the Report type)
const MAX_WINDOW_SECONDS = 900

// Collector is used to sample and store system metrics for reporting rolling
// averages over a period of time.
type Collector struct {
	// Options
	options struct {
		started         time.Time
		intervalSeconds int
		windowSeconds   int
		windowSize      int
	}
	// Timestamp of sample (unix milliseconds)
	timestamp []time.Time
	// CPU Usage (percentage)
	cpuUsage     []float64
	cpuCoreUsage [][]float64
	// Memory Usage (percentage)
	memoryUsage []float64
	// Network I/O (bytes)
	networkBytesSent     []float64
	networkBytesReceived []float64
	// Disk Usage (percentage)
	diskUsed       []float64
	diskInodesUsed []float64
	// Disk I/O (bytes)
	diskBytesRead    []float64
	diskBytesWritten []float64
}

// NewCollector creates a new metrics Collector.
// @param internval - the number of seconds between samples, zero means 5 secs.
func NewCollector(interval time.Duration) (*Collector, error) {
	// Set default values if not provided
	if interval.Seconds() <= 0 {
		interval = 5 * time.Second
	}
	intervalSeconds := int(interval.Seconds())
	// Check interval can be divided into MAX_WINDOW_SECONDS
	if MAX_WINDOW_SECONDS <= intervalSeconds || MAX_WINDOW_SECONDS%intervalSeconds != 0 {
		return nil, fmt.Errorf("interval size must be a divisor of %d", MAX_WINDOW_SECONDS)
	}
	// Get window size
	windowSize := MAX_WINDOW_SECONDS / intervalSeconds
	// Determine how many CPU Cores we need to track metrics for
	cpuInfo, err := cpu.Info()
	if err != nil {
		return nil, fmt.Errorf("failed to get CPU info: %w", err)
	}
	numCores := cpuInfo[0].Cores
	if numCores < 1 {
		return nil, fmt.Errorf("no CPU cores found")
	}
	// Initialise a slice of float64 for the number of CPU cores
	cpuCoreUsage := make([][]float64, numCores)
	for i := range numCores {
		cpuCoreUsage[i] = []float64{}
	}
	// Initialise all Collector slices and return
	return &Collector{
		options: struct {
			started         time.Time
			intervalSeconds int
			windowSeconds   int
			windowSize      int
		}{
			started:         time.Now().UTC(),
			intervalSeconds: intervalSeconds,
			windowSeconds:   MAX_WINDOW_SECONDS,
			windowSize:      windowSize,
		},
		timestamp:            []time.Time{},
		cpuUsage:             []float64{},
		cpuCoreUsage:         cpuCoreUsage,
		memoryUsage:          []float64{},
		networkBytesSent:     []float64{},
		networkBytesReceived: []float64{},
		diskUsed:             []float64{},
		diskInodesUsed:       []float64{},
		diskBytesRead:        []float64{},
		diskBytesWritten:     []float64{},
	}, nil
}

// Sample adds a sample to the Collector metrics, and unshifts the relevant
// slices if the window size is reached.
func (c *Collector) Sample() error {
	// Timestamp of sample
	c.timestamp = append(c.timestamp, time.Now().UTC())

	// CPU Usage (percentage) - using 10ms sampling period
	cpuPercentAll, err := cpu.Percent(10*time.Millisecond, false)
	if err != nil || len(cpuPercentAll) != 1 {
		return fmt.Errorf("failed to get CPU usage: %w", err)
	}
	c.cpuUsage = append(c.cpuUsage, cpuPercentAll[0])
	cpuPercentPerCore, err := cpu.Percent(100*time.Millisecond, true)
	if err != nil || len(cpuPercentPerCore) < 1 {
		return fmt.Errorf("failed to get per-core CPU usage: %w", err)
	}
	// Ensure we don't exceed the initialized slice capacity
	maxCores := len(c.cpuCoreUsage)
	if len(cpuPercentPerCore) > maxCores {
		cpuPercentPerCore = cpuPercentPerCore[:maxCores]
	}
	for i := range cpuPercentPerCore {
		c.cpuCoreUsage[i] = append(c.cpuCoreUsage[i], cpuPercentPerCore[i])
	}

	// Memory Usage (percentage)
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return fmt.Errorf("failed to get memory usage: %w", err)
	}
	c.memoryUsage = append(c.memoryUsage, memInfo.UsedPercent)

	// Network I/O (bytes) - across all interfaces
	netIOStats, err := net.IOCounters(false)
	if err != nil || len(netIOStats) != 1 {
		return fmt.Errorf("failed to get network I/O stats: %w", err)
	}
	c.networkBytesSent = append(c.networkBytesSent, float64(netIOStats[0].BytesSent))
	c.networkBytesReceived = append(c.networkBytesReceived, float64(netIOStats[0].BytesRecv))

	// Disk Usage (percentage) - for root partition
	diskUsage, err := disk.Usage("/")
	if err != nil {
		return fmt.Errorf("failed to get disk usage: %w", err)
	}
	c.diskUsed = append(c.diskUsed, diskUsage.UsedPercent)
	c.diskInodesUsed = append(c.diskInodesUsed, float64(diskUsage.InodesUsed))

	// Disk I/O (bytes) - aggregate across all disks
	diskIOStats, err := disk.IOCounters()
	if err != nil {
		return fmt.Errorf("failed to get disk I/O stats: %w", err)
	}
	var diskBytesRead, diskBytesWritten uint64
	for _, stat := range diskIOStats {
		diskBytesRead += stat.ReadBytes
		diskBytesWritten += stat.WriteBytes
	}
	c.diskBytesRead = append(c.diskBytesRead, float64(diskBytesRead))
	c.diskBytesWritten = append(c.diskBytesWritten, float64(diskBytesWritten))

	// Unshift all slices if the window size is reached
	if len(c.timestamp) > c.options.windowSize {
		c.timestamp = c.timestamp[1:]
		c.cpuUsage = c.cpuUsage[1:]
		for i := 0; i < len(c.cpuCoreUsage); i++ {
			c.cpuCoreUsage[i] = c.cpuCoreUsage[i][1:]
		}
		c.memoryUsage = c.memoryUsage[1:]
		c.networkBytesSent = c.networkBytesSent[1:]
		c.networkBytesReceived = c.networkBytesReceived[1:]
		c.diskUsed = c.diskUsed[1:]
		c.diskInodesUsed = c.diskInodesUsed[1:]
		c.diskBytesRead = c.diskBytesRead[1:]
		c.diskBytesWritten = c.diskBytesWritten[1:]
	}

	return nil
}

// Timing returns the timestamp of when the Collector was created plus the
// timestamp of the first and last sample in the window.
func (c Collector) Timing() struct {
	Started     time.Time
	WindowStart time.Time
	WindowEnd   time.Time
} {
	return struct {
		Started     time.Time
		WindowStart time.Time
		WindowEnd   time.Time
	}{
		Started:     c.options.started,
		WindowStart: c.timestamp[0],
		WindowEnd:   c.timestamp[len(c.timestamp)-1],
	}
}

// Averages returns a Metrics struct containing the averages of each metric over
// the sampling window `windowSeconds`.
func (c Collector) Averages(windowSeconds int) (Metrics, error) {
	// Check window seconds is valid (must be divisible by interval seconds)
	if windowSeconds <= 0 || windowSeconds > c.options.windowSeconds || windowSeconds%c.options.intervalSeconds != 0 {
		return Metrics{}, fmt.Errorf("window seconds must be divisible by %d and must not exceed %d", c.options.intervalSeconds, c.options.windowSeconds)
	}
	// Determine number of samples to average based on window seconds
	numSamples := windowSeconds / c.options.intervalSeconds
	// Calculate average per CPU core
	cpuCoreUsage := make([]float64, len(c.cpuCoreUsage))
	for i := range c.cpuCoreUsage {
		cpuCoreUsage[i] = meanLastN(c.cpuCoreUsage[i], numSamples)
	}
	// Calculate all other averages and return
	return Metrics{
		CPUUsage:             meanLastN(c.cpuUsage, numSamples),
		CPUCoreUsage:         cpuCoreUsage,
		MemoryUsage:          meanLastN(c.memoryUsage, numSamples),
		NetworkBytesSent:     meanLastN(c.networkBytesSent, numSamples),
		NetworkBytesReceived: meanLastN(c.networkBytesReceived, numSamples),
		DiskUsed:             meanLastN(c.diskUsed, numSamples),
		DiskInodesUsed:       meanLastN(c.diskInodesUsed, numSamples),
		DiskBytesRead:        meanLastN(c.diskBytesRead, numSamples),
		DiskBytesWritten:     meanLastN(c.diskBytesWritten, numSamples),
	}, nil
}

// meanLastN calculates the mean of the last N values in a slice.
// If the slice is empty or N is less than or equal to 0, it returns 0.
// If N is greater than the length of the slice, it calculates the mean of
// the entire slice.
// This function is used to calculate the average of the last N samples in
// the metrics collector.
func meanLastN(values []float64, n int) float64 {
	if len(values) == 0 || n <= 0 {
		return 0
	}
	if len(values) < n {
		n = len(values)
	}
	var sum float64
	for _, v := range values[len(values)-n:] {
		sum += v
	}
	result := sum / float64(n)
	return math.Round(result*100) / 100
}

// Start starts sampling the system metrics at the specified interval. It will
// stop when the context is cancelled.
func (c *Collector) Start(ctx context.Context, logger *slog.Logger) {
	// Set up ticker for sampling intervals
	ticker := time.NewTicker(time.Duration(c.options.intervalSeconds) * time.Second)
	defer ticker.Stop()

	// Start sampling
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Sample(); err != nil {
				logger.Error("failed to sample metrics", "err", err)
				return
			}
		}
	}
}

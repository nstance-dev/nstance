// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"log/slog"

	"github.com/nstance-dev/nstance/internal/buildvars"
	"github.com/shirou/gopsutil/v4/host"
)

// TerminationNotice represents a spot instance termination notice
type TerminationNotice struct {
	Action   string    `json:"action"`
	Deadline time.Time `json:"deadline,omitempty"`
}

// Report hold a report of metrics to be serialised to JSON by JSONPusher
type Report struct {
	InstanceID        string
	Version           string             `json:"version"`
	Timestamp         time.Time          `json:"timestamp"`
	Count             int                `json:"count"`
	Started           time.Time          `json:"started"`
	WindowStart       time.Time          `json:"window_start"`
	WindowEnd         time.Time          `json:"window_end"`
	Uptime            uint64             `json:"uptime"`
	OneMinute         Metrics            `json:"one_minute"`
	FiveMinutes       Metrics            `json:"five_minutes"`
	FifteenMinutes    Metrics            `json:"fifteen_minutes"`
	Files             map[string]string  `json:"files"`
	TerminationNotice *TerminationNotice `json:"termination_notice,omitempty"`
	ConfigHash        string             `json:"config_hash,omitempty"` // Runtime config hash
}

// ReportConfig provides the runtime config required to build metrics reports.
type ReportConfig struct {
	InstanceID  string
	RecvDir     string
	IdentityDir string // Path to identity directory for reading config.hash
	// GetTerminationNotice returns the current spot termination notice, if any
	GetTerminationNotice func() *TerminationNotice
}

// NewReport creates a new agent Report with averages from Collector metrics
func NewReport(collector *Collector, count int, cfg ReportConfig) (Report, error) {
	var err error

	// Validate required fields
	if strings.TrimSpace(cfg.InstanceID) == "" {
		return Report{}, fmt.Errorf("instance ID is required for health reports")
	}

	// Get build version as string
	buildVersion := buildvars.BuildVersion()

	// Uptime (seconds)
	hostInfo, err := host.Info()
	if err != nil {
		return Report{}, fmt.Errorf("failed to get uptime: %w", err)
	}

	// Get timing info
	timing := collector.Timing()

	// Calculate averages
	oneMinute, err := collector.Averages(60)
	if err != nil {
		return Report{}, fmt.Errorf("failed to get one minute averages: %w", err)
	}
	fiveMinutes, err := collector.Averages(300)
	if err != nil {
		return Report{}, fmt.Errorf("failed to get five minute averages: %w", err)
	}
	fifteenMinutes, err := collector.Averages(900)
	if err != nil {
		return Report{}, fmt.Errorf("failed to get fifteen minute averages: %w", err)
	}

	// Read config hash
	if cfg.IdentityDir == "" {
		return Report{}, fmt.Errorf("identity directory is required to read config hash")
	}
	configHashPath := filepath.Join(cfg.IdentityDir, "config.hash")
	data, err := os.ReadFile(configHashPath)
	if err != nil {
		return Report{}, fmt.Errorf("failed to read config hash from %s: %s", configHashPath, err)
	}
	configHash := strings.TrimSpace(string(data))

	// Initialise report and return
	report := Report{
		InstanceID:        cfg.InstanceID,
		Version:           buildVersion,
		Timestamp:         time.Now().UTC(),
		Count:             count,
		Started:           timing.Started,
		WindowStart:       timing.WindowStart,
		WindowEnd:         timing.WindowEnd,
		Uptime:            hostInfo.Uptime,
		OneMinute:         oneMinute,
		FiveMinutes:       fiveMinutes,
		FifteenMinutes:    fifteenMinutes,
		Files:             make(map[string]string),
		TerminationNotice: cfg.GetTerminationNotice(),
		ConfigHash:        configHash,
	}

	// If no receive directory is configured, return empty files map
	if cfg.RecvDir == "" {
		return report, nil
	}

	// Collect file status by scanning receive directory
	entries, err := os.ReadDir(cfg.RecvDir)
	if err != nil {
		// Directory doesn't exist or can't be read, return empty files map
		// This is not a fatal error as the agent may be starting up
		return report, nil
	}

	// First pass: collect error files from errors/ subdirectory
	errorsDir := filepath.Join(cfg.RecvDir, "errors")
	if errorEntries, err := os.ReadDir(errorsDir); err == nil {
		for _, entry := range errorEntries {
			errorFilename := entry.Name()
			errorPath := filepath.Join(errorsDir, errorFilename)
			if errorData, err := os.ReadFile(errorPath); err == nil {
				report.Files[errorFilename] = string(errorData)
			}
		}
	}

	// Second pass: collect regular files
	for _, entry := range entries {
		filename := entry.Name()

		// Skip internal metadata files and subdirectories
		if strings.HasPrefix(filename, ".") || entry.IsDir() {
			continue
		}

		// Regular file - only set timestamp if not already set (preserves errors)
		if _, exists := report.Files[filename]; exists {
			continue
		}

		filePath := filepath.Join(cfg.RecvDir, filename)
		if stat, err := os.Stat(filePath); err == nil {
			report.Files[filename] = stat.ModTime().UTC().Format(time.RFC3339)
		}
	}
	return report, nil
}

// ReportLoop collects metrics and invokes the publish callback on each interval.
func ReportLoop(ctx context.Context, logger *slog.Logger, reportInterval time.Duration, cfg ReportConfig, publish func(Report) error) {
	count := 0

	sampleInterval := time.Duration(1 * time.Second)

	// Create a collector
	collector, err := NewCollector(sampleInterval)
	if err != nil {
		logger.Error("failed to create metrics collector", "err", err)
		return
	}

	// Set up ticker for sampling intervals
	sampleTicker := time.NewTicker(sampleInterval)
	defer sampleTicker.Stop()

	// Set up ticker for reporting intervals
	reportTicker := time.NewTicker(reportInterval)
	defer reportTicker.Stop()

	// Start reporting
	for {
		select {
		case <-sampleTicker.C:
			if err := collector.Sample(); err != nil {
				logger.Error("failed to sample metrics", "err", err)
			}
		case <-reportTicker.C:
			count++
			report, err := NewReport(collector, count, cfg)
			if err != nil {
				logger.Error("failed to generate metrics report", "err", err)
				continue
			}
			if publish != nil {
				logger.Debug("publishing health report", "count", count, "instance_id", cfg.InstanceID)
				if err := publish(report); err != nil {
					logger.Error("failed to publish metrics report", "err", err)
				}
			}
		case <-ctx.Done():
			logger.Info("metrics reporter shutting down due to context cancellation")
			return
		}
	}
}

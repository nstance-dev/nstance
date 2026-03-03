// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package instanceinfo

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nstance-dev/nstance/pkg/health"
)

// SpotMonitor monitors for spot instance termination notices.
type SpotMonitor struct {
	client       *Client
	logger       *slog.Logger
	pollInterval time.Duration
	lastNotice   *health.TerminationNotice
	mu           sync.RWMutex
}

// SpotMonitorConfig configures the termination notice monitor.
type SpotMonitorConfig struct {
	PollInterval time.Duration
	Logger       *slog.Logger
}

// NewSpotMonitor creates a new termination notice monitor.
// It auto-detects the provider and spot status.
func NewSpotMonitor(cfg SpotMonitorConfig) (*SpotMonitor, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	client, err := New()
	if err != nil {
		cfg.Logger.Info("provider detection failed, disabling spot monitoring", "err", err)
		return &SpotMonitor{
			logger:       cfg.Logger,
			pollInterval: cfg.PollInterval,
		}, nil
	}

	// Check if running on spot instance
	isSpot, err := client.IsSpot(context.Background())
	if err != nil {
		cfg.Logger.Info("spot detection failed, disabling spot monitoring", "err", err)
		return &SpotMonitor{
			logger:       cfg.Logger,
			pollInterval: cfg.PollInterval,
		}, nil
	}

	if !isSpot {
		cfg.Logger.Info("not running on spot instance, spot monitoring disabled")
		return &SpotMonitor{
			logger:       cfg.Logger,
			pollInterval: cfg.PollInterval,
		}, nil
	}

	cfg.Logger.Info("running on spot instance, enabling termination monitoring",
		"provider", client.Provider(),
		"interval", cfg.PollInterval)

	return &SpotMonitor{
		client:       client,
		logger:       cfg.Logger,
		pollInterval: cfg.PollInterval,
	}, nil
}

// Start begins monitoring for termination notices.
func (m *SpotMonitor) Start(ctx context.Context) {
	if m.client == nil {
		return
	}
	if m.pollInterval == 0 {
		m.logger.Info("spot poll interval is 0, disabling termination monitoring")
		return
	}

	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("spot monitor shutting down")
			return
		case <-ticker.C:
			m.poll(ctx)
		}
	}
}

// poll checks for a termination notice.
func (m *SpotMonitor) poll(ctx context.Context) {
	notice, err := m.client.GetTerminationNotice(ctx)
	if err != nil {
		m.logger.Error("failed to get termination notice", "err", err)
		return
	}

	m.mu.Lock()
	m.lastNotice = notice
	m.mu.Unlock()

	if notice != nil {
		m.logger.Info("detected spot termination notice",
			"action", notice.Action,
			"deadline", notice.Deadline)
	}
}

// GetTerminationNotice returns the most recent termination notice.
func (m *SpotMonitor) GetTerminationNotice() *health.TerminationNotice {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastNotice
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package election

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nadrama-com/s3lect"

	"github.com/nstance-dev/nstance/internal/server/storage"
)

const leaderLockfilePath = "leader.json"

// ElectorFactory creates s3lect electors. Used for testing.
type ElectorFactory func(opts s3lect.S3ElectorOptions) (s3lect.Elector, error)

// defaultElectorFactory creates real S3 electors.
func defaultElectorFactory(opts s3lect.S3ElectorOptions) (s3lect.Elector, error) {
	return s3lect.NewS3Elector(opts)
}

// ManagerConfig contains configuration for the election manager.
type ManagerConfig struct {
	// ClusterID is the cluster identifier
	ClusterID string
	// ShardID is the shard identifier (e.g. zone shard ID)
	ShardID string
	// ServerID is this server's unique identifier
	ServerID string
	// ServerAddr is the address other servers use for peer verification
	ServerAddr string
	// Logger for election events
	Logger *slog.Logger
}

// ElectionConfig contains configuration for starting an individual election.
type ElectionConfig struct {
	// Storage is the s3lect-compatible storage for leader election state
	Storage storage.Storage
	// PeerMode enables peer verification of leadership
	PeerMode bool
	// CACert is the CA certificate for peer verification (nil if peer mode disabled)
	CACert []byte
	// FrequentInterval is how often to check leadership when actively participating
	FrequentInterval time.Duration
	// InfrequentInterval is how often to check leadership when idle
	InfrequentInterval time.Duration
	// LeaderTimeout is how long before a leader is considered dead
	LeaderTimeout time.Duration
	// OnAcquire is called after successfully acquiring leadership
	OnAcquire func(ctx context.Context) error
	// OnLose is called after losing leadership
	OnLose func(ctx context.Context) error
}

// Manager manages both cluster and shard leader elections.
type Manager struct {
	cfg ManagerConfig

	mu             sync.RWMutex
	clusterElector s3lect.Elector
	shardElector   s3lect.Elector
	healthServer   *healthServer
	electorFactory ElectorFactory
}

// NewManager creates a new election manager.
func NewManager(cfg ManagerConfig) *Manager {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Manager{
		cfg:            cfg,
		electorFactory: defaultElectorFactory,
	}
}

// StartClusterElection creates and starts the cluster leader election.
func (m *Manager) StartClusterElection(ctx context.Context, cfg ElectionConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.clusterElector != nil {
		return fmt.Errorf("cluster election already started")
	}

	if cfg.FrequentInterval == 0 {
		cfg.FrequentInterval = 5 * time.Second
	}
	if cfg.InfrequentInterval == 0 {
		cfg.InfrequentInterval = 30 * time.Second
	}
	if cfg.LeaderTimeout == 0 {
		cfg.LeaderTimeout = 15 * time.Second
	}

	electorID := m.cfg.ShardID + "-" + m.cfg.ServerID
	logger := m.cfg.Logger.With("election", "cluster", "elector_id", electorID)

	onAcquire := cfg.OnAcquire
	onLose := cfg.OnLose

	elector, err := m.electorFactory(s3lect.S3ElectorOptions{
		Config: &s3lect.ElectorConfig{
			LockfilePath:       leaderLockfilePath,
			ServerID:           electorID,
			ServerAddr:         m.cfg.ServerAddr,
			FrequentInterval:   cfg.FrequentInterval,
			InfrequentInterval: cfg.InfrequentInterval,
			LeaderTimeout:      cfg.LeaderTimeout,
			PeerMode:           cfg.PeerMode,
			PeerHealthPath:     "/health/leadership/cluster",
			PeerCACert:         cfg.CACert,
			OnAcquireLeadership: func(ctx context.Context) error {
				logger.Info("Acquired cluster leadership")
				if onAcquire != nil {
					return onAcquire(ctx)
				}
				return nil
			},
			OnLoseLeadership: func(ctx context.Context) error {
				logger.Warn("Lost cluster leadership")
				if onLose != nil {
					return onLose(ctx)
				}
				return nil
			},
		},
		Storage: cfg.Storage,
		Logger:  logger,
	})
	if err != nil {
		return fmt.Errorf("failed to create cluster elector: %w", err)
	}

	if err := elector.Start(ctx); err != nil {
		return fmt.Errorf("failed to start cluster election: %w", err)
	}

	m.clusterElector = elector
	return nil
}

// WaitForClusterElection waits for the first cluster election cycle to complete.
func (m *Manager) WaitForClusterElection(ctx context.Context) error {
	m.mu.RLock()
	elector := m.clusterElector
	m.mu.RUnlock()

	if elector == nil {
		return fmt.Errorf("cluster election not started")
	}

	if _, err := elector.WaitForNextElection(ctx, time.Time{}); err != nil {
		return fmt.Errorf("failed to complete initial cluster election: %w", err)
	}
	return nil
}

// EnableClusterPeerMode enables peer mode on the cluster elector with the given CA certificate.
func (m *Manager) EnableClusterPeerMode(caCert []byte) error {
	m.mu.RLock()
	elector := m.clusterElector
	m.mu.RUnlock()

	if elector == nil {
		return fmt.Errorf("cluster election not started")
	}

	return elector.EnablePeerMode(caCert)
}

// StartShardElection creates and starts the shard leader election.
func (m *Manager) StartShardElection(ctx context.Context, cfg ElectionConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shardElector != nil {
		return fmt.Errorf("shard election already started")
	}

	if cfg.FrequentInterval == 0 {
		cfg.FrequentInterval = 5 * time.Second
	}
	if cfg.InfrequentInterval == 0 {
		cfg.InfrequentInterval = 30 * time.Second
	}
	if cfg.LeaderTimeout == 0 {
		cfg.LeaderTimeout = 15 * time.Second
	}

	if len(cfg.CACert) == 0 {
		return fmt.Errorf("shard election requires CACert (peer mode is always enabled)")
	}
	cfg.PeerMode = true

	electorID := m.cfg.ServerID
	logger := m.cfg.Logger.With("election", "shard", "elector_id", electorID)

	onAcquire := cfg.OnAcquire
	onLose := cfg.OnLose

	elector, err := m.electorFactory(s3lect.S3ElectorOptions{
		Config: &s3lect.ElectorConfig{
			LockfilePath:       leaderLockfilePath,
			ServerID:           electorID,
			ServerAddr:         m.cfg.ServerAddr,
			FrequentInterval:   cfg.FrequentInterval,
			InfrequentInterval: cfg.InfrequentInterval,
			LeaderTimeout:      cfg.LeaderTimeout,
			PeerMode:           cfg.PeerMode,
			PeerHealthPath:     "/health/leadership/shard",
			PeerTimeout:        3 * time.Second,
			PeerCACert:         cfg.CACert,
			OnAcquireLeadership: func(ctx context.Context) error {
				logger.Info("Acquired shard leadership")
				if onAcquire != nil {
					return onAcquire(ctx)
				}
				return nil
			},
			OnLoseLeadership: func(ctx context.Context) error {
				logger.Warn("Lost shard leadership")
				if onLose != nil {
					return onLose(ctx)
				}
				return nil
			},
		},
		Storage: cfg.Storage,
		Logger:  logger,
	})
	if err != nil {
		return fmt.Errorf("failed to create shard elector: %w", err)
	}

	if err := elector.Start(ctx); err != nil {
		return fmt.Errorf("failed to start shard election: %w", err)
	}

	m.shardElector = elector
	return nil
}

// IsClusterLeader returns true if this server is the current cluster leader.
func (m *Manager) IsClusterLeader() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clusterElector != nil && m.clusterElector.IsLeader()
}

// IsShardLeader returns true if this server is the current shard leader.
func (m *Manager) IsShardLeader() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.shardElector != nil && m.shardElector.IsLeader()
}

// Stop stops all electors and the health server.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error

	if m.healthServer != nil {
		if err := m.healthServer.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("health server: %w", err))
		}
		m.healthServer = nil
	}

	if m.clusterElector != nil {
		if err := m.clusterElector.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("cluster elector: %w", err))
		}
		m.clusterElector = nil
	}

	if m.shardElector != nil {
		if err := m.shardElector.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("shard elector: %w", err))
		}
		m.shardElector = nil
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

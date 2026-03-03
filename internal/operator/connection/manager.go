// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package connection

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Manager manages persistent gRPC connections to all zone shards
type Manager struct {
	mu          sync.RWMutex
	connections map[string]*grpc.ClientConn
	tlsConfig   *tls.Config
	logger      logr.Logger
}

// NewManager creates a new connection manager
func NewManager(tlsConfig *tls.Config, logger logr.Logger) *Manager {
	return &Manager{
		connections: make(map[string]*grpc.ClientConn),
		tlsConfig:   tlsConfig,
		logger:      logger,
	}
}

// Connect establishes connections to all shard endpoints
func (m *Manager) Connect(ctx context.Context, endpoints map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for shard, endpoint := range endpoints {
		if _, exists := m.connections[shard]; exists {
			m.logger.Info("connection already exists", "shard", shard)
			continue
		}

		conn, err := m.connectToShard(ctx, shard, endpoint)
		if err != nil {
			errs = append(errs, fmt.Errorf("shard %s: %w", shard, err))
			continue
		}

		m.connections[shard] = conn
		m.logger.Info("connected to shard", "shard", shard, "endpoint", endpoint)
	}

	if len(errs) > 0 && len(m.connections) == 0 {
		return fmt.Errorf("failed to connect to any shard: %v", errs)
	}

	return nil
}

// connectToShard establishes a connection to a single shard
func (m *Manager) connectToShard(ctx context.Context, shard, endpoint string) (*grpc.ClientConn, error) {
	creds := credentials.NewTLS(m.tlsConfig)

	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection: %w", err)
	}

	return conn, nil
}

// GetAllConnections returns all shard connections.
func (m *Manager) GetAllConnections() map[string]*grpc.ClientConn {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conns := make(map[string]*grpc.ClientConn, len(m.connections))
	for shard, conn := range m.connections {
		conns[shard] = conn
	}
	return conns
}

// GetConnection returns a gRPC connection to any available shard.
func (m *Manager) GetConnection(ctx context.Context) (*grpc.ClientConn, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for shard, conn := range m.connections {
		return conn, shard, nil
	}

	return nil, "", fmt.Errorf("no connections available")
}

// Close closes all connections
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for shard, conn := range m.connections {
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("shard %s: %w", shard, err))
		}
		delete(m.connections, shard)
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing connections: %v", errs)
	}

	return nil
}

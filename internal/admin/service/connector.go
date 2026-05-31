// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"

	"github.com/nstance-dev/nstance/internal/proto"
)

// ShardServer represents a shard and its operator endpoint.
type ShardServer struct {
	ShardID string
	Address string // host:port for operator gRPC
}

// ParseServersFlag parses "shard1=host1:port1,shard2=host2:port2" format.
func ParseServersFlag(servers string) ([]ShardServer, error) {
	if servers == "" {
		return nil, fmt.Errorf("servers flag is empty")
	}

	var result []ShardServer
	for _, part := range strings.Split(servers, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		eqIdx := strings.Index(part, "=")
		if eqIdx == -1 {
			return nil, fmt.Errorf("invalid server format %q: expected shard=host:port", part)
		}

		shardID := strings.TrimSpace(part[:eqIdx])
		address := strings.TrimSpace(part[eqIdx+1:])

		if shardID == "" {
			return nil, fmt.Errorf("invalid server format %q: shard ID is empty", part)
		}
		if address == "" {
			return nil, fmt.Errorf("invalid server format %q: address is empty", part)
		}

		result = append(result, ShardServer{
			ShardID: shardID,
			Address: address,
		})
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no servers specified")
	}

	return result, nil
}

// Connector manages gRPC connections to nstance-servers.
type Connector struct {
	servers   []ShardServer
	tlsConfig *tls.Config
	timeout   time.Duration
	logger    *slog.Logger
	conns     []*Connection
}

// Connection represents an active gRPC connection to a shard server.
type Connection struct {
	Address string
	ShardID string
	Client  proto.OperatorServiceClient
	conn    *grpc.ClientConn
}

// Close closes the gRPC connection.
func (c *Connection) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// NewConnector creates a new Connector.
func NewConnector(servers []ShardServer, tlsConfig *tls.Config, timeout time.Duration, logger *slog.Logger) *Connector {
	return &Connector{
		servers:   servers,
		tlsConfig: tlsConfig,
		timeout:   timeout,
		logger:    logger,
	}
}

// ConnectAll connects to all servers in the list.
func (c *Connector) ConnectAll(ctx context.Context) ([]*Connection, error) {
	var connections []*Connection
	for _, server := range c.servers {
		conn, err := c.connect(ctx, server)
		if err != nil {
			// Close any connections we've already made
			for _, existing := range connections {
				_ = existing.Close()
			}
			return nil, fmt.Errorf("connect to %s (%s): %w", server.ShardID, server.Address, err)
		}
		connections = append(connections, conn)
	}
	c.conns = connections
	return connections, nil
}

// ConnectShard connects to a specific shard by ID.
func (c *Connector) ConnectShard(ctx context.Context, shardID string) (*Connection, error) {
	for _, server := range c.servers {
		if server.ShardID == shardID {
			conn, err := c.connect(ctx, server)
			if err != nil {
				return nil, fmt.Errorf("connect to %s (%s): %w", server.ShardID, server.Address, err)
			}
			c.conns = append(c.conns, conn)
			return conn, nil
		}
	}
	return nil, fmt.Errorf("shard %q not found in servers list", shardID)
}

// Servers returns the list of configured servers.
func (c *Connector) Servers() []ShardServer {
	return c.servers
}

// GetConnection returns a gRPC connection to any available shard.
// It tries existing connections first, then attempts to connect to servers.
func (c *Connector) GetConnection(ctx context.Context) (*grpc.ClientConn, string, error) {
	// Try existing connections first
	for _, conn := range c.conns {
		return conn.conn, conn.ShardID, nil
	}

	// No existing connections, try to connect to each server
	for _, server := range c.servers {
		conn, err := c.connect(ctx, server)
		if err != nil {
			c.logger.Warn("failed to connect to server", "shard", server.ShardID, "error", err)
			continue
		}
		c.conns = append(c.conns, conn)
		return conn.conn, conn.ShardID, nil
	}

	return nil, "", fmt.Errorf("failed to connect to any server")
}

// Close closes all connections.
func (c *Connector) Close() {
	for _, conn := range c.conns {
		_ = conn.Close()
	}
	c.conns = nil
}

func (c *Connector) connect(ctx context.Context, server ShardServer) (*Connection, error) {
	creds := credentials.NewTLS(c.tlsConfig)
	conn, err := grpc.NewClient(server.Address, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, err
	}

	// Trigger connection and wait for it to be ready
	conn.Connect()
	if !conn.WaitForStateChange(ctx, connectivity.Idle) {
		_ = conn.Close()
		return nil, ctx.Err()
	}

	return &Connection{
		Address: server.Address,
		ShardID: server.ShardID,
		Client:  proto.NewOperatorServiceClient(conn),
		conn:    conn,
	}, nil
}

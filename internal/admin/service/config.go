// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
)

// ConfigStatusRequest contains parameters for checking config status.
type ConfigStatusRequest struct {
	Shard     string
	AllShards bool
}

// ShardStatus represents the configuration status of a single shard.
type ShardStatus struct {
	Shard        string
	Etag         string
	LastModified time.Time
	Size         int64
	Error        error
}

// ConfigStatusResponse contains status for one or more shards.
type ConfigStatusResponse struct {
	Shards []ShardStatus
}

// ConfigRefreshRequest contains parameters for refreshing config.
type ConfigRefreshRequest struct {
	Shard     string
	AllShards bool
}

// ShardRefreshResult represents the result of refreshing a single shard.
type ShardRefreshResult struct {
	Shard   string
	Updated bool
	Etag    string
	Error   error
}

// ConfigRefreshResponse contains results for one or more shards.
type ConfigRefreshResponse struct {
	Shards []ShardRefreshResult
}

// ConfigService manages configuration operations on Nstance servers.
type ConfigService struct {
	connector *Connector
}

// NewConfigService creates a new ConfigService.
func NewConfigService(connector *Connector) *ConfigService {
	return &ConfigService{
		connector: connector,
	}
}

// Status retrieves configuration status from one or more shards.
func (s *ConfigService) Status(ctx context.Context, req ConfigStatusRequest) (*ConfigStatusResponse, error) {
	connections, err := s.resolveConnections(ctx, req.Shard, req.AllShards)
	if err != nil {
		return nil, err
	}

	resp := &ConfigStatusResponse{
		Shards: make([]ShardStatus, 0, len(connections)),
	}

	for _, conn := range connections {
		status := s.getShardStatus(ctx, conn)
		resp.Shards = append(resp.Shards, status)
	}

	return resp, nil
}

// Refresh triggers configuration refresh on one or more shards.
func (s *ConfigService) Refresh(ctx context.Context, req ConfigRefreshRequest) (*ConfigRefreshResponse, error) {
	connections, err := s.resolveConnections(ctx, req.Shard, req.AllShards)
	if err != nil {
		return nil, err
	}

	resp := &ConfigRefreshResponse{
		Shards: make([]ShardRefreshResult, 0, len(connections)),
	}

	for _, conn := range connections {
		result := s.refreshShard(ctx, conn)
		resp.Shards = append(resp.Shards, result)
	}

	return resp, nil
}

func (s *ConfigService) resolveConnections(ctx context.Context, shard string, allShards bool) ([]*Connection, error) {
	if shard != "" && allShards {
		return nil, fmt.Errorf("shard and all-shards are mutually exclusive")
	}
	if shard == "" && !allShards {
		return nil, fmt.Errorf("must specify shard or all-shards")
	}

	if allShards {
		return s.connector.ConnectAll(ctx)
	}

	conn, err := s.connector.ConnectShard(ctx, shard)
	if err != nil {
		return nil, err
	}
	return []*Connection{conn}, nil
}

func (s *ConfigService) getShardStatus(ctx context.Context, conn *Connection) ShardStatus {
	status := ShardStatus{Shard: conn.ShardID}

	resp, err := conn.Client.GetConfigStatus(ctx, &emptypb.Empty{})
	if err != nil {
		status.Error = fmt.Errorf("get status: %w", err)
		return status
	}

	status.Etag = resp.Etag
	status.LastModified = resp.LastModified.AsTime()
	status.Size = resp.Size
	return status
}

func (s *ConfigService) refreshShard(ctx context.Context, conn *Connection) ShardRefreshResult {
	result := ShardRefreshResult{Shard: conn.ShardID}

	resp, err := conn.Client.RefreshConfig(ctx, &emptypb.Empty{})
	if err != nil {
		result.Error = fmt.Errorf("refresh: %w", err)
		return result
	}

	result.Updated = resp.Updated
	result.Etag = resp.Etag
	return result
}

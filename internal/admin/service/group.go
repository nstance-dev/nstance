// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
)

// GroupScaleRequest contains parameters for scaling a group.
type GroupScaleRequest struct {
	Shard     string
	AllShards bool
	Group     string
	Size      int32
}

// GroupScaleResult represents the result of scaling a group.
type GroupScaleResult struct {
	Shard string
	Group string
	Size  int32
	Error error
}

// GroupScaleResponse contains results for one or more shards.
type GroupScaleResponse struct {
	Results []GroupScaleResult
}

// GroupService manages group operations on Nstance servers.
type GroupService struct {
	connector *Connector
}

// NewGroupService creates a new GroupService.
func NewGroupService(connector *Connector) *GroupService {
	return &GroupService{
		connector: connector,
	}
}

// Scale updates the size of a group on one or more shards.
func (s *GroupService) Scale(ctx context.Context, req GroupScaleRequest) (*GroupScaleResponse, error) {
	connections, err := s.resolveConnections(ctx, req.Shard, req.AllShards)
	if err != nil {
		return nil, err
	}

	resp := &GroupScaleResponse{
		Results: make([]GroupScaleResult, 0, len(connections)),
	}

	for _, conn := range connections {
		result := s.scaleShard(ctx, conn, req.Group, req.Size)
		resp.Results = append(resp.Results, result)
	}

	return resp, nil
}

func (s *GroupService) resolveConnections(ctx context.Context, shard string, allShards bool) ([]*Connection, error) {
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

func (s *GroupService) scaleShard(ctx context.Context, conn *Connection, group string, size int32) GroupScaleResult {
	result := GroupScaleResult{Shard: conn.ShardID, Group: group, Size: size}

	_, err := conn.Client.UpsertGroup(ctx, &proto.UpsertGroupRequest{
		Key: group,
		Config: &proto.GroupConfig{
			Size: size,
		},
	})
	if err != nil {
		result.Error = fmt.Errorf("scale: %w", err)
		return result
	}

	return result
}

// GroupListRequest contains parameters for listing groups.
type GroupListRequest struct {
	Shard     string
	AllShards bool
}

// GroupListResult represents a group from a shard.
type GroupListResult struct {
	Shard        string
	Key          string
	Size         int32
	Template     string
	InstanceType string
	IsStatic     bool
	Error        error
}

// GroupListResponse contains results for one or more shards.
type GroupListResponse struct {
	Results []GroupListResult
}

// List returns all groups from one or more shards.
func (s *GroupService) List(ctx context.Context, req GroupListRequest) (*GroupListResponse, error) {
	connections, err := s.resolveConnections(ctx, req.Shard, req.AllShards)
	if err != nil {
		return nil, err
	}

	resp := &GroupListResponse{
		Results: make([]GroupListResult, 0),
	}

	for _, conn := range connections {
		results := s.listShard(ctx, conn)
		resp.Results = append(resp.Results, results...)
	}

	return resp, nil
}

func (s *GroupService) listShard(ctx context.Context, conn *Connection) []GroupListResult {
	listResp, err := conn.Client.ListGroups(ctx, &emptypb.Empty{})
	if err != nil {
		return []GroupListResult{{Shard: conn.ShardID, Error: fmt.Errorf("list: %w", err)}}
	}

	results := make([]GroupListResult, 0, len(listResp.Groups))
	for _, g := range listResp.Groups {
		results = append(results, GroupListResult{
			Shard:        conn.ShardID,
			Key:          g.Key,
			Size:         g.Size,
			Template:     g.Template,
			InstanceType: g.InstanceType,
			IsStatic:     g.IsStatic,
		})
	}

	return results
}

// GroupCreateRequest contains parameters for creating a group.
type GroupCreateRequest struct {
	Shard        string
	AllShards    bool
	Group        string
	Template     string
	Size         int32
	InstanceType string
	SubnetPool   string
	Vars         map[string]string
}

// GroupCreateResult represents the result of creating a group on a shard.
type GroupCreateResult struct {
	Shard string
	Group string
	Size  int32
	Error error
}

// GroupCreateResponse contains results for one or more shards.
type GroupCreateResponse struct {
	Results []GroupCreateResult
}

// Create creates a new dynamic group on one or more shards.
func (s *GroupService) Create(ctx context.Context, req GroupCreateRequest) (*GroupCreateResponse, error) {
	connections, err := s.resolveConnections(ctx, req.Shard, req.AllShards)
	if err != nil {
		return nil, err
	}

	resp := &GroupCreateResponse{
		Results: make([]GroupCreateResult, 0, len(connections)),
	}

	for _, conn := range connections {
		result := s.createShard(ctx, conn, req)
		resp.Results = append(resp.Results, result)
	}

	return resp, nil
}

func (s *GroupService) createShard(ctx context.Context, conn *Connection, req GroupCreateRequest) GroupCreateResult {
	result := GroupCreateResult{Shard: conn.ShardID, Group: req.Group, Size: req.Size}

	grpCfg := &proto.GroupConfig{
		Template:     req.Template,
		Size:         req.Size,
		InstanceType: req.InstanceType,
		SubnetPool:   req.SubnetPool,
		Vars:         req.Vars,
	}

	_, err := conn.Client.UpsertGroup(ctx, &proto.UpsertGroupRequest{
		Key:    req.Group,
		Config: grpCfg,
	})
	if err != nil {
		result.Error = fmt.Errorf("create: %w", err)
		return result
	}

	return result
}

// GroupStatusRequest contains parameters for getting group status.
type GroupStatusRequest struct {
	Shard     string
	AllShards bool
	Group     string
}

// GroupStatusResult represents detailed status of a group from a shard.
type GroupStatusResult struct {
	Shard        string
	Key          string
	Size         int32
	Template     string
	InstanceType string
	SubnetPool   string
	Vars         map[string]string
	IsStatic     bool
	Error        error
}

// GroupStatusResponse contains results for one or more shards.
type GroupStatusResponse struct {
	Results []GroupStatusResult
}

// Status returns detailed status of a specific group from one or more shards.
func (s *GroupService) Status(ctx context.Context, req GroupStatusRequest) (*GroupStatusResponse, error) {
	connections, err := s.resolveConnections(ctx, req.Shard, req.AllShards)
	if err != nil {
		return nil, err
	}

	resp := &GroupStatusResponse{
		Results: make([]GroupStatusResult, 0),
	}

	for _, conn := range connections {
		result := s.statusShard(ctx, conn, req.Group)
		resp.Results = append(resp.Results, result)
	}

	return resp, nil
}

func (s *GroupService) statusShard(ctx context.Context, conn *Connection, group string) GroupStatusResult {
	listResp, err := conn.Client.ListGroups(ctx, &emptypb.Empty{})
	if err != nil {
		return GroupStatusResult{Shard: conn.ShardID, Key: group, Error: fmt.Errorf("list: %w", err)}
	}

	for _, g := range listResp.Groups {
		if g.Key == group {
			return GroupStatusResult{
				Shard:        conn.ShardID,
				Key:          g.Key,
				Size:         g.Size,
				Template:     g.Template,
				InstanceType: g.InstanceType,
				SubnetPool:   g.SubnetPool,
				Vars:         g.Vars,
				IsStatic:     g.IsStatic,
			}
		}
	}

	return GroupStatusResult{Shard: conn.ShardID, Key: group, Error: fmt.Errorf("group not found")}
}

// GroupDeleteRequest contains parameters for deleting a group.
type GroupDeleteRequest struct {
	Shard     string
	AllShards bool
	Group     string
}

// GroupDeleteResult represents the result of deleting a group on a shard.
type GroupDeleteResult struct {
	Shard string
	Group string
	Error error
}

// GroupDeleteResponse contains results for one or more shards.
type GroupDeleteResponse struct {
	Results []GroupDeleteResult
}

// Delete removes a dynamic group from one or more shards.
func (s *GroupService) Delete(ctx context.Context, req GroupDeleteRequest) (*GroupDeleteResponse, error) {
	connections, err := s.resolveConnections(ctx, req.Shard, req.AllShards)
	if err != nil {
		return nil, err
	}

	resp := &GroupDeleteResponse{
		Results: make([]GroupDeleteResult, 0, len(connections)),
	}

	for _, conn := range connections {
		result := s.deleteShard(ctx, conn, req.Group)
		resp.Results = append(resp.Results, result)
	}

	return resp, nil
}

func (s *GroupService) deleteShard(ctx context.Context, conn *Connection, group string) GroupDeleteResult {
	result := GroupDeleteResult{Shard: conn.ShardID, Group: group}

	_, err := conn.Client.DeleteGroup(ctx, &proto.DeleteGroupRequest{
		Key: group,
	})
	if err != nil {
		result.Error = fmt.Errorf("delete: %w", err)
		return result
	}

	return result
}

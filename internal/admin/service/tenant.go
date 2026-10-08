// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/v2/internal/identifiers"
	"github.com/nstance-dev/nstance/v2/internal/proto"
)

// tenantConnector supplies independently selected shard connections.
type tenantConnector interface {
	ConnectShard(context.Context, string) (*Connection, error)
	Servers() []ShardServer
}

// TenantRequest selects a tenant and shards, with a timeout for each shard.
// Force and WakeAt apply only to sleep operations.
type TenantRequest struct {
	Tenant    string
	Shard     string
	AllShards bool
	Timeout   time.Duration
	Force     bool
	WakeAt    *time.Time
}

// Validate checks the request before loading an identity or contacting servers.
func (r TenantRequest) Validate(servers []ShardServer) error {
	if err := identifiers.Validate("tenant ID", r.Tenant); err != nil {
		return err
	}
	if (r.Shard == "" && !r.AllShards) || (r.Shard != "" && r.AllShards) {
		return fmt.Errorf("must specify exactly one of shard or all-shards")
	}
	if r.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if !r.AllShards {
		found := false
		for _, server := range servers {
			found = found || server.ShardID == r.Shard
		}
		if !found {
			return fmt.Errorf("shard %q not found in servers list", r.Shard)
		}
	}
	if r.WakeAt != nil {
		if err := timestamppb.New(*r.WakeAt).CheckValid(); err != nil {
			return fmt.Errorf("invalid wake deadline: %w", err)
		}
	}
	return nil
}

// TenantResult is one shard's outcome; an error does not roll back other shards.
type TenantResult struct {
	Shard     string
	Status    proto.TenantSleepStatus
	Result    string
	WakeAt    *timestamppb.Timestamp
	Listeners []*proto.ListenerActivity
	Error     error
}

// TenantService manages independent tenant sleep and wake operations.
type TenantService struct {
	connector tenantConnector
}

// NewTenantService creates a service using the caller's shard connections.
func NewTenantService(connector tenantConnector) *TenantService {
	return &TenantService{connector: connector}
}

// Status reads tenant sleep state and listener activity on the selected shards.
func (s *TenantService) Status(ctx context.Context, req TenantRequest) ([]TenantResult, error) {
	return s.run(ctx, req, "status")
}

// Sleep sleeps the tenant, guarding against activity unless Force is set.
func (s *TenantService) Sleep(ctx context.Context, req TenantRequest) ([]TenantResult, error) {
	return s.run(ctx, req, "sleep")
}

// Wake wakes the tenant and resumes group reconciliation on the selected shards.
func (s *TenantService) Wake(ctx context.Context, req TenantRequest) ([]TenantResult, error) {
	return s.run(ctx, req, "wake")
}

// run attempts each selected shard even when an earlier shard fails.
func (s *TenantService) run(ctx context.Context, req TenantRequest, operation string) ([]TenantResult, error) {
	if err := req.Validate(s.connector.Servers()); err != nil {
		return nil, err
	}
	targets := []ShardServer{{ShardID: req.Shard}}
	if req.AllShards {
		targets = s.connector.Servers()
	}
	results := make([]TenantResult, 0, len(targets))
	for _, target := range targets {
		shardCtx, cancel := context.WithTimeout(ctx, req.Timeout)
		result := TenantResult{Shard: target.ShardID}
		connection, err := s.connector.ConnectShard(shardCtx, target.ShardID)
		if err == nil {
			result, err = s.runShard(shardCtx, connection, req, operation)
		}
		cancel()
		if err != nil {
			result.Error = fmt.Errorf("%s: tenant %s %s: %w", target.ShardID, req.Tenant, operation, err)
		}
		results = append(results, result)
	}
	return results, nil
}

// runShard maps a tenant operation to its RPC and interprets rejected transitions.
func (s *TenantService) runShard(ctx context.Context, connection *Connection, req TenantRequest, operation string) (TenantResult, error) {
	result := TenantResult{Shard: connection.ShardID}
	switch operation {
	case "sleep":
		var wakeAt *timestamppb.Timestamp
		if req.WakeAt != nil {
			wakeAt = timestamppb.New(*req.WakeAt)
		}
		response, err := connection.Client.SleepTenant(ctx, &proto.SleepTenantRequest{Tenant: req.Tenant, IfNotBusy: !req.Force, WakeAt: wakeAt})
		if err != nil {
			return result, err
		}
		if response.GetResult() != proto.SleepTenantResponse_RESULT_SLEPT && response.GetResult() != proto.SleepTenantResponse_RESULT_ALREADY_ASLEEP {
			return result, fmt.Errorf("sleep rejected: %s", response.GetResult())
		}
		result.Status, result.Result, result.WakeAt = response.GetStatus(), response.GetResult().String(), response.GetWakeAt()
	case "wake":
		response, err := connection.Client.WakeTenant(ctx, &proto.WakeTenantRequest{Tenant: req.Tenant})
		if err != nil {
			return result, err
		}
		if response.GetResult() != proto.WakeTenantResponse_RESULT_WOKE && response.GetResult() != proto.WakeTenantResponse_RESULT_ALREADY_AWAKE {
			return result, fmt.Errorf("wake rejected: %s", response.GetResult())
		}
		result.Status, result.Result = response.GetStatus(), response.GetResult().String()
	case "status":
		response, err := connection.Client.GetTenantStatus(ctx, &proto.GetTenantStatusRequest{Tenant: req.Tenant})
		if err != nil {
			return result, err
		}
		result.Status, result.WakeAt, result.Listeners = response.GetStatus(), response.GetWakeAt(), response.GetListeners()
	}
	return result, nil
}

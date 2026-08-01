// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/identifiers"
	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/tenantstate"
)

// SleepTenant marks a tenant as asleep.
func (s *Service) SleepTenant(ctx context.Context, req *proto.SleepTenantRequest) (*proto.SleepTenantResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	tenant, err := s.authorizedTenant(ctx, req.GetTenant())
	if err != nil {
		return nil, err
	}
	if !s.sleepReady {
		return nil, status.Error(codes.FailedPrecondition, "tenant sleep is unavailable until provider cutover is configured")
	}
	if req.IfNotBusy {
		return nil, status.Error(codes.FailedPrecondition, "guarded sleep is unavailable until provider cutover is configured")
	}
	if s.tenantState == nil {
		return nil, status.Error(codes.FailedPrecondition, "tenant state is unavailable")
	}
	var wakeAt *time.Time
	if req.WakeAt != nil {
		if err := req.WakeAt.CheckValid(); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid wake_at: %v", err)
		}
		value := req.WakeAt.AsTime().UTC()
		wakeAt = &value
	}
	alreadyAsleep, effectiveWakeAt, err := s.tenantState.Sleep(ctx, tenant, wakeAt)
	if err != nil {
		if errors.Is(err, tenantstate.ErrInactive) || errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Unavailable, "shard leadership changed")
		}
		return nil, status.Errorf(codes.Internal, "failed to persist tenant sleep state: %v", err)
	}
	result := proto.SleepTenantResponse_RESULT_SLEPT
	if alreadyAsleep {
		result = proto.SleepTenantResponse_RESULT_ALREADY_ASLEEP
	}
	response := &proto.SleepTenantResponse{
		Result: result,
		Status: proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP,
	}
	if effectiveWakeAt != nil {
		response.WakeAt = timestamppb.New(*effectiveWakeAt)
	}
	return response, nil
}

// WakeTenant marks a tenant as awake.
func (s *Service) WakeTenant(ctx context.Context, req *proto.WakeTenantRequest) (*proto.WakeTenantResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	tenant, err := s.authorizedTenant(ctx, req.GetTenant())
	if err != nil {
		return nil, err
	}
	if !s.sleepReady {
		return nil, status.Error(codes.FailedPrecondition, "tenant wake is unavailable until provider cutover is configured")
	}
	if req.Listener != nil {
		return nil, status.Error(codes.FailedPrecondition, "listener-scoped wake is not available until proxy listeners are configured")
	}
	if s.tenantState == nil {
		return nil, status.Error(codes.FailedPrecondition, "tenant state is unavailable")
	}
	alreadyAwake, err := s.tenantState.Wake(ctx, tenant)
	if err != nil {
		if errors.Is(err, tenantstate.ErrInactive) || errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Unavailable, "shard leadership changed")
		}
		return nil, status.Errorf(codes.Internal, "failed to persist tenant wake state: %v", err)
	}
	result := proto.WakeTenantResponse_RESULT_WOKE
	if alreadyAwake {
		result = proto.WakeTenantResponse_RESULT_ALREADY_AWAKE
	}
	return &proto.WakeTenantResponse{
		Result: result,
		Status: proto.TenantSleepStatus_TENANT_SLEEP_STATUS_AWAKE,
	}, nil
}

// authorizedTenant validates that the authenticated operator owns the requested tenant.
func (s *Service) authorizedTenant(ctx context.Context, requested string) (string, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}
	if requested == "" {
		return "", status.Error(codes.InvalidArgument, "tenant is required")
	}
	if err := identifiers.Validate("tenant ID", requested); err != nil {
		return "", status.Errorf(codes.InvalidArgument, "invalid tenant %q: %v", requested, err)
	}
	if clientInfo.Tenant != requested {
		return "", status.Error(codes.PermissionDenied, "tenant does not match operator certificate")
	}
	return requested, nil
}

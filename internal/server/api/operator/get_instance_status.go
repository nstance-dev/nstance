// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"database/sql"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/instances"
)

func (s *Service) GetInstanceStatus(ctx context.Context, req *proto.GetInstanceStatusRequest) (*proto.InstanceStatusResponse, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	s.logger.Info("Getting instance status", "client_id", clientInfo.ClientID, "instance_id", req.InstanceId)

	if req.InstanceId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "instance_id is required")
	}
	instanceStatus, err := s.instanceManager.GetInstanceStatus(ctx, clientInfo.Tenant, req.InstanceId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, instances.ErrInstanceTenantMismatch) {
			return nil, tenantInstanceError(err)
		}
		s.logger.Error("Failed to get instance status",
			"client_id", clientInfo.ClientID,
			"instance_id", req.InstanceId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to get instance status: %v", err)
	}

	resp := &proto.InstanceStatusResponse{
		InstanceId:         instanceStatus.InstanceID,
		Status:             instanceStatus.Status,
		ProviderInstanceId: instanceStatus.ProviderInstanceID,
	}

	if !instanceStatus.CreatedAt.IsZero() {
		resp.CreatedAt = timestamppb.New(instanceStatus.CreatedAt)
	}

	if !instanceStatus.LastUpdated.IsZero() {
		resp.LastSeen = timestamppb.New(instanceStatus.LastUpdated)
	}

	s.logger.Info("Retrieved instance status",
		"client_id", clientInfo.ClientID,
		"instance_id", instanceStatus.InstanceID,
		"status", instanceStatus.Status)

	return resp, nil
}

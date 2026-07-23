// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/infra"
	"github.com/nstance-dev/nstance/internal/server/instances"
)

func (s *Service) DeleteInstance(ctx context.Context, req *proto.DeleteInstanceRequest) (*proto.DeleteInstanceResponse, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	s.logger.Info("Deleting instance", "client_id", clientInfo.ClientID, "instance_id", req.InstanceId)

	if req.InstanceId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "instance_id is required")
	}

	instanceStatus, err := s.instanceManager.GetInstanceStatus(ctx, clientInfo.Tenant, req.InstanceId)
	if err != nil {
		if errors.Is(err, instances.ErrInstanceTenantMismatch) {
			return nil, tenantInstanceError(err)
		}
		s.logger.Info("Instance not found during deletion (idempotent)",
			"client_id", clientInfo.ClientID,
			"instance_id", req.InstanceId)
		return &proto.DeleteInstanceResponse{Status: infra.StatusUnknown}, nil
	}

	if instanceStatus.Status == infra.StatusDeleted || instanceStatus.Status == infra.StatusDeleting {
		s.logger.Info("Instance already in terminal deletion state",
			"client_id", clientInfo.ClientID,
			"instance_id", req.InstanceId,
			"status", instanceStatus.Status)
		return &proto.DeleteInstanceResponse{Status: instanceStatus.Status}, nil
	}

	err = s.instanceManager.DeleteInstance(ctx, clientInfo.Tenant, req.InstanceId)
	if err != nil {
		if errors.Is(err, instances.ErrInstanceTenantMismatch) {
			return nil, tenantInstanceError(err)
		}
		s.logger.Error("Failed to delete instance",
			"client_id", clientInfo.ClientID,
			"instance_id", req.InstanceId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to delete instance: %v", err)
	}

	s.logger.Info("Instance deletion initiated",
		"client_id", clientInfo.ClientID,
		"instance_id", req.InstanceId)

	return &proto.DeleteInstanceResponse{
		Status: infra.StatusDeleting,
	}, nil
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/instances"
)

func (s *Service) CreateInstance(ctx context.Context, req *proto.CreateInstanceRequest) (*proto.CreateInstanceResponse, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	s.logger.Info("Creating instance", "client_id", clientInfo.ClientID, "instance_id", req.InstanceId)

	if req.Config == nil {
		return nil, status.Errorf(codes.InvalidArgument, "instance config is required")
	}
	if req.Config.Group == "" {
		return nil, status.Errorf(codes.InvalidArgument, "group is required")
	}

	createReq := instances.CreateInstanceRequest{
		InstanceID:   req.InstanceId,
		Group:        req.Config.Group,
		Template:     req.Config.Template,
		InstanceType: req.Config.InstanceType,
		SubnetPool:   req.Config.GetSubnetPool(),
		Vars:         req.Config.Vars,
		OnDemand:     true,
	}

	resp, err := s.instanceManager.CreateInstance(ctx, createReq)
	if err != nil {
		s.logger.Error("Failed to create instance",
			"client_id", clientInfo.ClientID,
			"group", req.Config.Group,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to create instance: %v", err)
	}

	s.logger.Info("Instance created successfully",
		"client_id", clientInfo.ClientID,
		"instance_id", resp.InstanceID,
		"provider_instance_id", resp.ProviderInstanceID,
		"status", resp.Status)

	return &proto.CreateInstanceResponse{
		InstanceId: resp.InstanceID,
		Status:     resp.Status,
	}, nil
}

// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
)

func (s *Service) AcknowledgeDrained(ctx context.Context, req *proto.DrainAckRequest) (*emptypb.Empty, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	if req.InstanceId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "instance_id is required")
	}

	s.logger.Info("Operator acknowledged drain",
		"client_id", clientInfo.ClientID,
		"instance_id", req.InstanceId)

	s.onDrainAcked(req.InstanceId)

	return &emptypb.Empty{}, nil
}

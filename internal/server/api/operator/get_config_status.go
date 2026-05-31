// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
)

func (s *Service) GetConfigStatus(ctx context.Context, req *emptypb.Empty) (*proto.ConfigStatusResponse, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	s.logger.Info("Getting config status", "client_id", clientInfo.ClientID)

	metadata := s.configLoader.GetMetadata()
	if metadata == nil {
		s.logger.Warn("No configuration metadata available", "client_id", clientInfo.ClientID)
		return nil, status.Errorf(codes.Internal, "no configuration metadata available")
	}

	return &proto.ConfigStatusResponse{
		Etag:         metadata.ETag,
		LastModified: timestamppb.New(metadata.LastModified),
		Size:         metadata.Size,
	}, nil
}

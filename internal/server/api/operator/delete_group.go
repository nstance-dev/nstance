// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/identifiers"
	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	serverconfig "github.com/nstance-dev/nstance/internal/server/config"
)

func (s *Service) DeleteGroup(ctx context.Context, req *proto.DeleteGroupRequest) (*emptypb.Empty, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	tenant := clientInfo.Tenant
	s.logger.Info("Deleting group", "client_id", clientInfo.ClientID, "tenant", tenant, "group", req.Key)

	if req.Key == "" {
		return nil, status.Errorf(codes.InvalidArgument, "group key is required")
	}
	if err := identifiers.Validate("tenant ID", tenant); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid tenant %q: %v", tenant, err)
	}
	if err := identifiers.Validate("group ID", req.Key); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid group key %q: %v", req.Key, err)
	}

	s.groupMutationMu.Lock()
	defer s.groupMutationMu.Unlock()

	if err := serverconfig.DeleteGroup(ctx, s.configLoader, tenant, req.Key); err != nil {
		s.logger.Error("Failed to delete group", "client_id", clientInfo.ClientID, "tenant", tenant, "group", req.Key, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to delete group: %v", err)
	}

	s.onGroupChanged(tenant, req.Key)

	staticGroup, isStatic := s.configLoader.GetCurrent().Groups[tenant][req.Key]
	if isStatic {
		s.NotifyGroupEvent(tenant, &proto.GroupEvent{
			Type:  proto.GroupEvent_UPSERT,
			Group: s.buildGroupStatus(tenant, req.Key, staticGroup, true),
		})
	} else {
		s.NotifyGroupEvent(tenant, &proto.GroupEvent{
			Type:  proto.GroupEvent_DELETE,
			Group: &proto.GroupStatus{Key: req.Key, Tenant: tenant},
		})
	}

	s.logger.Info("Group deleted successfully", "client_id", clientInfo.ClientID, "group", req.Key)
	return &emptypb.Empty{}, nil
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/internal/identifiers"
	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	serverconfig "github.com/nstance-dev/nstance/internal/server/config"
)

func (s *Service) UpsertGroup(ctx context.Context, req *proto.UpsertGroupRequest) (*proto.GroupStatus, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	tenant := clientInfo.Tenant
	s.logger.Info("Upserting group", "client_id", clientInfo.ClientID, "tenant", tenant, "group", req.Key)

	if req.Key == "" {
		return nil, status.Errorf(codes.InvalidArgument, "group key is required")
	}
	if err := identifiers.Validate("tenant ID", tenant); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid tenant %q: %v", tenant, err)
	}
	if err := identifiers.Validate("group ID", req.Key); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid group key %q: %v", req.Key, err)
	}
	if req.Config == nil {
		return nil, status.Errorf(codes.InvalidArgument, "group config is required")
	}

	size := int(req.Config.Size)
	groupConfig := serverconfig.GroupConfig{
		Template:     req.Config.Template,
		Size:         &size,
		InstanceType: req.Config.InstanceType,
		SubnetPool:   req.Config.GetSubnetPool(),
		Vars:         req.Config.Vars,
	}

	s.groupMutationMu.Lock()
	defer s.groupMutationMu.Unlock()

	if err := serverconfig.UpsertGroup(ctx, s.configLoader, tenant, req.Key, groupConfig); err != nil {
		s.logger.Error("Failed to upsert group", "client_id", clientInfo.ClientID, "tenant", tenant, "group", req.Key, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to upsert group: %v", err)
	}

	s.onGroupChanged(tenant, req.Key)

	// Load the merged group to return actual values
	mergedGroup, err := serverconfig.GetGroup(ctx, s.configLoader, tenant, req.Key)
	if err != nil {
		s.logger.Error("Failed to get merged group", "client_id", clientInfo.ClientID, "tenant", tenant, "group", req.Key, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to get merged group: %v", err)
	}

	// Determine if this is a static group
	staticGroups := s.configLoader.GetCurrent().Groups[tenant]
	_, isStatic := staticGroups[req.Key]

	groupStatus := s.buildGroupStatus(tenant, req.Key, *mergedGroup, isStatic)

	s.NotifyGroupEvent(tenant, &proto.GroupEvent{
		Type:  proto.GroupEvent_TYPE_UPSERT,
		Group: groupStatus,
	})

	s.logger.Info("Group upserted successfully", "client_id", clientInfo.ClientID, "tenant", tenant, "group", req.Key)
	return groupStatus, nil
}

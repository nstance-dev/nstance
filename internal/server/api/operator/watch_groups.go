// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
)

// WatchGroups streams group change events to the operator.
// On connection, sends all current groups as UPSERT events (initial snapshot).
func (s *Service) WatchGroups(req *emptypb.Empty, stream proto.OperatorService_WatchGroupsServer) error {
	clientInfo, err := api.GetClientInfo(stream.Context())
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	s.streamMu.Lock()
	s.groupsStream = stream
	s.streamMu.Unlock()

	defer func() {
		s.streamMu.Lock()
		if s.groupsStream == stream {
			s.groupsStream = nil
		}
		s.streamMu.Unlock()
	}()

	tenant := clientInfo.Tenant
	s.logger.Info("Operator connected to groups stream", "client_id", clientInfo.ClientID, "tenant", tenant)

	// Send current state as initial snapshot
	groups, err := s.listGroups(tenant)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to list groups: %v", err)
	}

	for _, g := range groups {
		event := &proto.GroupEvent{
			Type:  proto.GroupEvent_UPSERT,
			Group: g,
		}
		if err := stream.Send(event); err != nil {
			return err
		}
	}

	s.logger.Info("Sent groups snapshot", "client_id", clientInfo.ClientID, "count", len(groups))

	<-stream.Context().Done()
	s.logger.Info("Operator disconnected from groups stream", "client_id", clientInfo.ClientID)
	return nil
}

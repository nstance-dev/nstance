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

	tenant := clientInfo.Tenant
	registeredStream := &groupsStream{stream: stream}
	registeredStream.mu.Lock()
	s.streamMu.Lock()
	s.groupsStreams[tenant] = registeredStream
	s.streamMu.Unlock()

	defer func() {
		s.streamMu.Lock()
		if s.groupsStreams[tenant] == registeredStream {
			delete(s.groupsStreams, tenant)
		}
		s.streamMu.Unlock()
	}()

	s.logger.Info("Operator connected to groups stream", "client_id", clientInfo.ClientID, "tenant", tenant)

	// Send current state as initial snapshot
	groups, err := s.listGroups(tenant)
	if err != nil {
		registeredStream.mu.Unlock()
		return status.Errorf(codes.Internal, "failed to list groups: %v", err)
	}
	for _, g := range groups {
		event := &proto.GroupEvent{
			Type:  proto.GroupEvent_TYPE_UPSERT,
			Group: g,
		}
		err := registeredStream.stream.Send(event)
		if err != nil {
			registeredStream.mu.Unlock()
			return err
		}
	}
	registeredStream.mu.Unlock()

	s.logger.Info("Sent groups snapshot", "client_id", clientInfo.ClientID, "count", len(groups))

	<-stream.Context().Done()
	s.logger.Info("Operator disconnected from groups stream", "client_id", clientInfo.ClientID)
	return nil
}

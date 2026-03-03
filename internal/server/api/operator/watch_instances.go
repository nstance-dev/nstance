// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	serverconfig "github.com/nstance-dev/nstance/internal/server/config"
)

// WatchInstances streams instance events to the operator for drain coordination.
// On connection, sends all pending drain instances as initial snapshot.
func (s *Service) WatchInstances(req *emptypb.Empty, stream proto.OperatorService_WatchInstancesServer) error {
	clientInfo, err := api.GetClientInfo(stream.Context())
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	s.streamMu.Lock()
	s.instancesStream = stream
	s.streamMu.Unlock()

	defer func() {
		s.streamMu.Lock()
		if s.instancesStream == stream {
			s.instancesStream = nil
		}
		s.streamMu.Unlock()
	}()

	s.logger.Info("Operator connected to instances stream", "client_id", clientInfo.ClientID)

	// Send initial snapshot of pending drain instances
	instances, err := s.localDB.GetInstancesPendingDrain(clientInfo.Tenant)
	if err != nil {
		s.logger.Error("Failed to get pending drain instances", "client_id", clientInfo.ClientID, "error", err)
		return status.Errorf(codes.Internal, "failed to get snapshot: %v", err)
	}

	cfg := s.configLoader.GetCurrent()

	for _, inst := range instances {
		if inst.DrainStartedAt == nil {
			continue
		}

		drainTimeout := cfg.Shard.DefaultDrainTimeout
		if inst.Tenant != "" && inst.Group != "" {
			group, err := serverconfig.GetGroup(stream.Context(), s.configLoader, inst.Tenant, inst.Group)
			if err == nil && group.DrainTimeout != nil {
				drainTimeout = *group.DrainTimeout
			}
		}

		deleteAt := inst.DrainStartedAt.Add(drainTimeout.Duration())

		event := &proto.InstanceEvent{
			InstanceId:         inst.ID,
			Tenant:             inst.Tenant,
			Group:              inst.Group,
			Status:             "pending_deletion",
			Reason:             "unhealthy",
			UnhealthyAt:        timestamppb.New(*inst.DrainStartedAt),
			DeleteAt:           timestamppb.New(deleteAt),
			ProviderInstanceId: ptrToString(inst.ProviderID),
		}

		if err := stream.Send(event); err != nil {
			s.logger.Warn("Failed to send snapshot event", "error", err)
			return err
		}
	}

	s.logger.Info("Sent drain snapshot", "client_id", clientInfo.ClientID, "count", len(instances))

	<-stream.Context().Done()
	s.logger.Info("Operator disconnected from instances stream", "client_id", clientInfo.ClientID)
	return nil
}

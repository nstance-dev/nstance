// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
)

// ReceiveFiles provides a persistent stream of files to be sent to the agent
func (s *Service) ReceiveFiles(req *emptypb.Empty, stream proto.AgentService_ReceiveFilesServer) error {
	clientInfo, err := api.GetClientInfo(stream.Context())
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	instanceID := clientInfo.ClientID
	s.logger.Info("Starting file transfer stream", "instance_id", instanceID)

	// Send any pending files immediately
	if err := s.sendPendingFiles(instanceID, stream); err != nil {
		return err
	}

	// Keep stream open and wait for context cancellation (client disconnect)
	<-stream.Context().Done()
	s.logger.Info("File transfer stream closed", "instance_id", instanceID)
	return nil
}

// sendPendingFiles sends all pending files for an instance
func (s *Service) sendPendingFiles(instanceID string, stream proto.AgentService_ReceiveFilesServer) error {
	pendingFiles := s.getPendingFiles(instanceID)
	if len(pendingFiles) == 0 {
		s.logger.Debug("No pending files for instance", "instance_id", instanceID)
		return nil
	}

	s.logger.Info("Streaming pending files",
		"instance_id", instanceID,
		"file_count", len(pendingFiles))

	// Get current group runtime config hash to send with files
	instance, err := s.localDB.GetInstance(instanceID)
	if err != nil {
		s.logger.Warn("Failed to get instance for config hash", "instance_id", instanceID, "error", err)
	}
	var configHash string
	if instance != nil {
		if group, err := s.localDB.GetGroup(instance.Tenant, instance.Group); err == nil && group != nil && group.RuntimeConfigHash != nil {
			configHash = *group.RuntimeConfigHash
		}
	}

	// Stream each pending file
	for _, file := range pendingFiles {
		fileTransfer := &proto.FileTransfer{
			Filename:     file.Filename,
			Content:      file.Content,
			LastModified: timestamppb.New(file.LastModified),
			ConfigHash:   configHash,
		}

		if err := stream.Send(fileTransfer); err != nil {
			s.logger.Error("Failed to stream file",
				"instance_id", instanceID,
				"filename", file.Filename,
				"error", err)
			return status.Errorf(codes.Internal, "failed to stream file: %v", err)
		}

		s.logger.Debug("Streamed file",
			"instance_id", instanceID,
			"filename", file.Filename)
	}

	// Clear pending files after successful delivery
	s.clearPendingFiles(instanceID)

	s.logger.Info("File transfer completed",
		"instance_id", instanceID,
		"files_delivered", len(pendingFiles))

	return nil
}

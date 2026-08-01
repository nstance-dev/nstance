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
	notify := s.registerPendingFilesStream(instanceID)
	defer s.unregisterPendingFilesStream(instanceID, notify)

	for {
		// Send pending files immediately, then wait to be notified about new work.
		active, err := s.sendPendingFiles(instanceID, notify, stream)
		if err != nil {
			return err
		}
		if !active {
			return nil
		}

		select {
		case <-notify:
			continue
		case <-stream.Context().Done():
			// Keep stream open until the client disconnects.
			s.logger.Info("File transfer stream closed", "instance_id", instanceID)
			return nil
		}
	}
}

// sendPendingFiles sends and clears one pending file patch while the stream is current.
func (s *Service) sendPendingFiles(instanceID string, owner chan struct{}, stream proto.AgentService_ReceiveFilesServer) (bool, error) {
	s.pendingFilesMu.RLock()
	if s.pendingFilesNotify[instanceID] != owner {
		s.pendingFilesMu.RUnlock()
		return false, nil
	}
	pending := s.pendingFiles[instanceID]
	s.pendingFilesMu.RUnlock()
	if pending == nil {
		return true, nil
	}

	s.logger.Info("Streaming pending files",
		"instance_id", instanceID,
		"file_count", len(pending.Files))

	for _, file := range pending.Files {
		if err := stream.Send(&proto.FileTransfer{
			Filename:     file.Filename,
			Content:      file.Content,
			LastModified: timestamppb.New(file.LastModified),
		}); err != nil {
			s.logger.Error("Failed to stream file",
				"instance_id", instanceID,
				"filename", file.Filename,
				"error", err)
			return true, status.Errorf(codes.Internal, "failed to stream file: %v", err)
		}

		s.logger.Debug("Streamed file",
			"instance_id", instanceID,
			"filename", file.Filename)
	}
	if err := stream.Send(&proto.FileTransfer{ConfigHash: pending.ConfigHash}); err != nil {
		s.logger.Error("Failed to commit file patch", "instance_id", instanceID, "error", err)
		return true, status.Errorf(codes.Internal, "failed to commit file patch: %v", err)
	}

	// Do not clear a newer transfer that replaced this one while it was sent.
	s.pendingFilesMu.Lock()
	if s.pendingFilesNotify[instanceID] == owner && s.pendingFiles[instanceID] == pending {
		delete(s.pendingFiles, instanceID)
	}
	s.pendingFilesMu.Unlock()

	s.logger.Info("File patch completed",
		"instance_id", instanceID,
		"files_delivered", len(pending.Files))

	return true, nil
}

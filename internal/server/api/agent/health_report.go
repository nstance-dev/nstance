// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/config"
)

// SubmitHealthReport processes health reports from agents via persistent stream
func (s *Service) SubmitHealthReport(stream proto.AgentService_SubmitHealthReportServer) error {
	clientInfo, err := api.GetClientInfo(stream.Context())
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	instanceID := clientInfo.ClientID
	s.logger.Info("Agent health stream connected", "instance_id", instanceID)

	// Monitor stream context for disconnection
	streamCtx := stream.Context()
	go func() {
		<-streamCtx.Done()

		// Determine disconnect type
		err := streamCtx.Err()
		graceful := err == context.Canceled

		if graceful {
			s.logger.Info("Agent health stream gracefully closed", "instance_id", instanceID)
		} else {
			s.logger.Warn("Agent health stream disconnected unexpectedly",
				"instance_id", instanceID,
				"error", err)
		}

		// Always trigger health check - agent is down regardless of how it disconnected
		if s.onInstanceDisconnect != nil {
			if err := s.onInstanceDisconnect(instanceID, graceful); err != nil {
				s.logger.Error("Failed to handle instance disconnect",
					"instance_id", instanceID,
					"graceful", graceful,
					"error", err)
			}
		}
	}()

	// Receive health reports from stream
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// Client closed stream gracefully
			s.logger.Info("Agent closed health stream", "instance_id", instanceID)
			return stream.SendAndClose(&emptypb.Empty{})
		}
		if err != nil {
			s.logger.Error("Error receiving health report",
				"instance_id", instanceID,
				"error", err)
			return status.Errorf(codes.Internal, "failed to receive health report: %v", err)
		}

		// Verify instance ID matches authenticated client
		if clientInfo.ClientID != req.InstanceId {
			s.logger.Warn("Health report instance ID mismatch",
				"authenticated_client", clientInfo.ClientID,
				"reported_instance", req.InstanceId)
			return status.Errorf(codes.PermissionDenied, "instance ID mismatch")
		}

		s.logger.Debug("Processing health report",
			"instance_id", req.InstanceId,
			"count", req.Count,
			"uptime", req.Uptime,
			"files_count", len(req.Files))

		// Process health report (existing logic)
		if err := s.processHealthReport(req); err != nil {
			s.logger.Error("Failed to process health report",
				"instance_id", req.InstanceId,
				"error", err)
			// Don't return error - continue receiving reports
		}
	}
}

// processHealthReport extracts the existing health report processing logic
func (s *Service) processHealthReport(req *proto.HealthReportRequest) error {
	// Store health record in SQLite
	if err := s.storeHealthRecord(req); err != nil {
		return fmt.Errorf("failed to store health record: %w", err)
	}

	// Convert proto file statuses to file processor format
	fileStatuses := convertProtoFileStatuses(req.Files)

	// Get instance information
	instance, err := s.localDB.GetInstance(req.InstanceId)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	// Read the config and effective group together so the
	// desired hash cannot combine values from either side of a concurrent reload.
	cfg, groupConfig, err := config.GetConfigAndGroup(s.configLoader, instance.Tenant, instance.Group)
	if err != nil {
		return fmt.Errorf("failed to get config and group: %w", err)
	}
	templateName := groupConfig.Template

	// Get template config
	template, exists := cfg.Templates[templateName]
	if !exists {
		return fmt.Errorf("template %s not found", templateName)
	}
	mergedConfig, err := cfg.GetMergedConfig(templateName, *groupConfig)
	if err != nil {
		return fmt.Errorf("failed to merge template and group config: %w", err)
	}
	runtimeHash := config.HashRuntimeConfig(*mergedConfig)

	// Build list of files that are required (either missing, or have errors)
	// Check both: files reported with errors AND files from template that weren't reported at all
	var filesRequired []string
	for filename := range template.Files {
		status, reported := fileStatuses[filename]
		if !reported || status.IsError() || status.IsMissing() {
			filesRequired = append(filesRequired, filename)
		}
	}
	if req.ConfigHash != runtimeHash {
		// The server cannot tell which rendered files changed, so regenerate all
		// configured files when the runtime configuration hash changes.
		s.requestFiles(req.InstanceId, runtimeHash, nil)
	} else if len(filesRequired) > 0 {
		s.requestFiles(req.InstanceId, runtimeHash, filesRequired)
	}

	// Check for missing keys and send additional key requests if needed
	go func() {
		if err := s.checkAndRequestMissingKeys(req.InstanceId); err != nil {
			s.logger.Error("Failed to check and request missing keys",
				"instance_id", req.InstanceId,
				"error", err)
		}
	}()

	// Handle spot instance termination notice
	if req.TerminationNotice != nil {
		go func() {
			if err := s.handleSpotTermination(req.InstanceId, req.TerminationNotice); err != nil {
				s.logger.Error("Failed to handle spot termination",
					"instance_id", req.InstanceId,
					"error", err)
			}
		}()
	}

	if s.onHealthReport != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := s.onHealthReport(ctx, req.InstanceId); err != nil {
				s.logger.Error("Failed to handle health report callback",
					"instance_id", req.InstanceId,
					"error", err)
			}
		}()
	}

	// Handle config drift detection
	go func() {
		driftCtx, driftCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer driftCancel()

		if err := s.handleConfigDrift(driftCtx, req.InstanceId, req.ConfigHash); err != nil {
			s.logger.Error("Failed to handle config drift",
				"instance_id", req.InstanceId,
				"error", err)
		}
	}()

	s.logger.Info("Health report processed successfully",
		"instance_id", req.InstanceId,
		"count", req.Count)

	return nil
}

func (s *Service) storeHealthRecord(req *proto.HealthReportRequest) error {
	healthRecord := map[string]interface{}{
		"timestamp":     req.Timestamp.AsTime().UTC(),
		"count":         req.Count,
		"uptime":        req.Uptime,
		"files":         convertFileStatusMap(req.Files),
		"last_reported": time.Now().UTC(),
		"metrics":       req.Metrics,
	}

	healthData, err := json.Marshal(healthRecord)
	if err != nil {
		return fmt.Errorf("failed to marshal health record: %w", err)
	}

	return s.localDB.UpdateInstanceHealth(req.InstanceId, healthData)
}

// FileStatus represents the status of a file from health reports
type FileStatus interface {
	IsError() bool
	IsMissing() bool
}

// ProtoFileStatus adapts proto.FileStatus to FileStatus interface
type ProtoFileStatus struct {
	status *proto.FileStatus
}

func (p *ProtoFileStatus) IsError() bool {
	_, isError := p.status.Status.(*proto.FileStatus_Error)
	return isError
}

func (p *ProtoFileStatus) IsMissing() bool {
	return p.status.Status == nil
}

func convertProtoFileStatuses(protoFiles map[string]*proto.FileStatus) map[string]FileStatus {
	result := make(map[string]FileStatus)
	for filename, status := range protoFiles {
		result[filename] = &ProtoFileStatus{status: status}
	}
	return result
}

func convertFileStatusMap(protoFiles map[string]*proto.FileStatus) map[string]interface{} {
	files := make(map[string]interface{})

	for filename, fileStatus := range protoFiles {
		switch status := fileStatus.Status.(type) {
		case *proto.FileStatus_LastModified:
			files[filename] = status.LastModified.AsTime().UTC().Format(time.RFC3339)
		case *proto.FileStatus_Error:
			files[filename] = fmt.Sprintf("error: %s", status.Error)
		default:
			files[filename] = nil
		}
	}

	return files
}

// handleConfigDrift checks for config drift and triggers appropriate actions
func (s *Service) handleConfigDrift(_ context.Context, instanceID, reportedConfigHash string) error {
	// Get instance from database
	instance, err := s.localDB.GetInstance(instanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}
	if instance == nil {
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	// Get group hashes
	group, err := s.configLoader.GetCachedGroup(instance.Tenant, instance.Group)
	if err != nil {
		return fmt.Errorf("failed to get group hashes: %w", err)
	}
	if group == nil {
		// No group hashes yet - config may not be loaded
		s.logger.Debug("No group hashes found, skipping drift detection", "group", instance.Group)
		return nil
	}

	// Check runtime config drift
	if group.RuntimeConfigHash != nil && *group.RuntimeConfigHash != reportedConfigHash {
		s.logger.Info("Runtime config drift detected",
			"instance_id", instanceID,
			"group", instance.Group,
			"current_hash", *group.RuntimeConfigHash,
			"reported_hash", reportedConfigHash)

	}

	// Check infra config drift (only if not already draining)
	if instance.DrainStartedAt == nil && group.InfraConfigHash != nil {
		instanceInfraHash := ""
		if instance.InfraConfigHash != nil {
			instanceInfraHash = *instance.InfraConfigHash
		}

		if *group.InfraConfigHash != instanceInfraHash {
			s.logger.Info("Infra config drift detected",
				"instance_id", instanceID,
				"group", instance.Group,
				"current_hash", *group.InfraConfigHash,
				"instance_hash", instanceInfraHash)

			// Trigger reconciliation to schedule rotation
			if s.onReconcileRequested != nil {
				if err := s.onReconcileRequested(instance.Tenant, instance.Group, "config-drift"); err != nil {
					s.logger.Error("Failed to trigger reconciliation for infra drift",
						"instance_id", instanceID,
						"group", instance.Group,
						"error", err)
				}
			}
		}
	}

	return nil
}

// handleSpotTermination processes spot instance termination notices
func (s *Service) handleSpotTermination(instanceID string, notice *proto.TerminationNotice) error {
	s.logger.Info("Handling spot termination notice",
		"instance_id", instanceID,
		"action", notice.Action)

	if s.onSpotTermination != nil {
		return s.onSpotTermination(instanceID, notice)
	}

	s.logger.Warn("No spot termination handler configured, ignoring notice",
		"instance_id", instanceID)
	return nil
}

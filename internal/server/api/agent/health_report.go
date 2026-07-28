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
	"github.com/nstance-dev/nstance/internal/server/infra"
	"github.com/nstance-dev/nstance/internal/server/localdb"
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

	// Determine which files need to be generated
	// Get configuration
	cfg := s.configLoader.GetCurrent()
	if cfg == nil {
		return fmt.Errorf("no configuration available")
	}

	// Get instance information
	instance, err := s.localDB.GetInstance(req.InstanceId)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	// Derive template from instance group (merge static + dynamic groups)
	groups, err := config.GetGroups(context.Background(), s.configLoader, instance.Tenant)
	if err != nil {
		return fmt.Errorf("failed to get groups: %w", err)
	}
	groupConfig, exists := groups[instance.Group]
	if !exists {
		return fmt.Errorf("instance group %s not found", instance.Group)
	}
	templateName := groupConfig.Template

	// Get template config
	template, exists := cfg.Templates[templateName]
	if !exists {
		return fmt.Errorf("template %s not found", templateName)
	}

	// Build list of files that are required (either missing, or have errors)
	// Check both: files reported with errors AND files from template that weren't reported at all
	var filesRequired []string
	for filename := range template.Files {
		status, reported := fileStatuses[filename]
		if !reported || status.IsError() || status.IsMissing() {
			filesRequired = append(filesRequired, filename)
		}
	}

	// Do not regenerate files that are already queued for delivery.
	if len(filesRequired) > 0 {
		pendingFiles := s.getPendingFiles(req.InstanceId)
		if len(pendingFiles) > 0 {
			pendingByName := make(map[string]bool, len(pendingFiles))
			for _, file := range pendingFiles {
				pendingByName[file.Filename] = true
			}

			filteredFiles := filesRequired[:0]
			for _, filename := range filesRequired {
				if !pendingByName[filename] {
					filteredFiles = append(filteredFiles, filename)
				}
			}
			filesRequired = filteredFiles
		}
	}

	// Process missing files asynchronously with proper context
	if len(filesRequired) > 0 {
		go func() {
			// Use background context with timeout for async processing
			processCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			generatedFiles, err := s.fileGenerator.GenerateFiles(processCtx, req.InstanceId, filesRequired)
			if err != nil {
				s.logger.Error("Failed to generate files",
					"instance_id", req.InstanceId,
					"error", err)
			} else if len(generatedFiles) > 0 {
				// Queue files for delivery
				for filename, content := range generatedFiles {
					s.QueueFile(req.InstanceId, filename, content)
				}
			}
		}()
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

	// Handle load balancer group registration on first successful health report
	if len(cfg.LoadBalancers) > 0 {
		go func() {
			lbCtx, lbCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer lbCancel()

			if err := s.handleLBRegistration(lbCtx, req.InstanceId); err != nil {
				s.logger.Error("Failed to handle load balancer registration",
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

// handleLBRegistration handles load balancer registration for an instance on health report
func (s *Service) handleLBRegistration(ctx context.Context, instanceID string) error {
	// Get instance from database
	instance, err := s.localDB.GetInstance(instanceID)
	if err != nil {
		return fmt.Errorf("getting instance: %w", err)
	}
	if instance == nil {
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	// Get current configuration
	cfg := s.configLoader.GetCurrent()

	// Get group configuration for this tenant
	tenantGroups := cfg.Groups[instance.Tenant]
	if tenantGroups == nil {
		s.logger.Debug("No tenant configuration for instance",
			"instance_id", instanceID,
			"tenant", instance.Tenant)
		return nil
	}
	groupConfig, exists := tenantGroups[instance.Group]
	if !exists {
		s.logger.Debug("No group configuration for instance",
			"instance_id", instanceID,
			"tenant", instance.Tenant,
			"group", instance.Group)
		return nil
	}

	// Register with load balancers if configured
	if len(groupConfig.LoadBalancers) > 0 {
		if err := s.registerInstanceWithLB(ctx, instance, groupConfig, cfg); err != nil {
			return fmt.Errorf("registering instance with LB: %w", err)
		}
	}

	// Reconcile any pending/failed registrations
	if len(cfg.LoadBalancers) > 0 {
		if err := s.reconcilePendingLBRegistrations(ctx, cfg); err != nil {
			s.logger.Warn("Failed to reconcile pending LB group registrations",
				"instance_id", instanceID,
				"error", err)
			// Don't return error - this is best-effort
		}
	}

	return nil
}

// registerInstanceWithLB registers an instance with all configured load balancers for its group
func (s *Service) registerInstanceWithLB(ctx context.Context, instance *localdb.Instance, groupConfig config.GroupConfig, cfg *config.Config) error {
	if len(groupConfig.LoadBalancers) == 0 {
		return nil
	}

	if instance.ProviderID == nil {
		return fmt.Errorf("instance has no provider ID")
	}

	s.logger.Info("Registering instance with load balancers",
		"instance_id", instance.ID,
		"provider_instance_id", *instance.ProviderID,
		"group", instance.Group,
		"load_balancers", len(groupConfig.LoadBalancers))

	for _, lbKey := range groupConfig.LoadBalancers {
		lbConfig, exists := cfg.LoadBalancers[lbKey]
		if !exists {
			s.logger.Error("Load balancer not found in config",
				"instance_id", instance.ID,
				"lb_key", lbKey)
			continue
		}
		if lbConfig.Provider == "tunnel" {
			continue
		}

		existing, err := s.localDB.GetLBInstance(lbKey, instance.ID)
		if err != nil {
			s.logger.Error("Failed to check existing LB registration",
				"instance_id", instance.ID,
				"lb_key", lbKey,
				"error", err)
			continue
		}

		if existing != nil && existing.Status == localdb.LBStatusRegistered {
			s.logger.Debug("Instance already registered with LB",
				"instance_id", instance.ID,
				"lb_key", lbKey)
			continue
		}

		if err := s.localDB.UpsertLBInstance(lbKey, instance.ID, localdb.LBStatusPending); err != nil {
			s.logger.Error("Failed to create pending LB registration",
				"instance_id", instance.ID,
				"lb_key", lbKey,
				"error", err)
			continue
		}

		req := infra.RegisterLBRequest{
			ProviderInstanceID: *instance.ProviderID,
			LBConfig:           infra.LoadBalancerConfigForProvider(lbConfig),
			Zone:               cfg.Shard.Infra.Zone,
		}

		if err := s.provider.RegisterWithLB(ctx, req); err != nil {
			s.logger.Error("Failed to register instance with load balancer",
				"instance_id", instance.ID,
				"provider_instance_id", *instance.ProviderID,
				"lb_key", lbKey,
				"error", err)

			if err := s.localDB.UpsertLBInstance(lbKey, instance.ID, localdb.LBStatusFailed); err != nil {
				s.logger.Error("Failed to update LB registration status to failed",
					"instance_id", instance.ID,
					"lb_key", lbKey,
					"error", err)
			}
			continue
		}

		if err := s.localDB.UpsertLBInstance(lbKey, instance.ID, localdb.LBStatusRegistered); err != nil {
			s.logger.Error("Failed to update LB registration status to registered",
				"instance_id", instance.ID,
				"lb_key", lbKey,
				"error", err)
		} else {
			s.logger.Info("Successfully registered instance with load balancer",
				"instance_id", instance.ID,
				"provider_instance_id", *instance.ProviderID,
				"lb_key", lbKey)
		}
	}

	return nil
}

// reconcilePendingLBRegistrations retries any pending or failed LB registrations
func (s *Service) reconcilePendingLBRegistrations(ctx context.Context, cfg *config.Config) error {
	pendingLBs, err := s.localDB.GetPendingOrFailedLBInstances()
	if err != nil {
		return fmt.Errorf("getting pending/failed LB instances: %w", err)
	}

	if len(pendingLBs) == 0 {
		return nil
	}

	s.logger.Info("Reconciling pending/failed load balancer registrations",
		"count", len(pendingLBs))

	for _, lbInstance := range pendingLBs {
		instance, err := s.localDB.GetInstance(lbInstance.InstanceID)
		if err != nil {
			s.logger.Error("Failed to get instance for LB reconciliation",
				"instance_id", lbInstance.InstanceID,
				"error", err)
			continue
		}

		if instance == nil || instance.ProviderID == nil {
			s.logger.Warn("Instance not found or has no provider ID, skipping LB reconciliation",
				"instance_id", lbInstance.InstanceID)
			continue
		}

		lbConfig, exists := cfg.LoadBalancers[lbInstance.LBKey]
		if !exists {
			s.logger.Warn("Load balancer not found in config",
				"instance_id", lbInstance.InstanceID,
				"lb_key", lbInstance.LBKey)
			continue
		}
		if lbConfig.Provider == "tunnel" {
			continue
		}

		s.logger.Info("Retrying load balancer registration",
			"instance_id", instance.ID,
			"provider_instance_id", *instance.ProviderID,
			"lb_key", lbInstance.LBKey)

		req := infra.RegisterLBRequest{
			ProviderInstanceID: *instance.ProviderID,
			LBConfig:           infra.LoadBalancerConfigForProvider(lbConfig),
			Zone:               cfg.Shard.Infra.Zone,
		}

		if err := s.provider.RegisterWithLB(ctx, req); err != nil {
			s.logger.Error("Failed to register instance with load balancer (retry)",
				"instance_id", instance.ID,
				"provider_instance_id", *instance.ProviderID,
				"lb_key", lbInstance.LBKey,
				"error", err)

			if err := s.localDB.UpsertLBInstance(lbInstance.LBKey, instance.ID, localdb.LBStatusFailed); err != nil {
				s.logger.Error("Failed to update LB registration status to failed",
					"instance_id", instance.ID,
					"lb_key", lbInstance.LBKey,
					"error", err)
			}
			continue
		}

		if err := s.localDB.UpsertLBInstance(lbInstance.LBKey, instance.ID, localdb.LBStatusRegistered); err != nil {
			s.logger.Error("Failed to update LB registration status to registered",
				"instance_id", instance.ID,
				"lb_key", lbInstance.LBKey,
				"error", err)
		} else {
			s.logger.Info("Successfully registered instance with load balancer (retry)",
				"instance_id", instance.ID,
				"provider_instance_id", *instance.ProviderID,
				"lb_key", lbInstance.LBKey)
		}
	}

	return nil
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
	group, err := s.localDB.GetGroup(instance.Tenant, instance.Group)
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

		// Regenerate all files (hash will be sent automatically when files stream)
		generatedFiles, err := s.fileGenerator.GenerateFiles(context.Background(), instanceID, nil)
		if err != nil {
			s.logger.Error("Failed to generate files for drift update", "instance_id", instanceID, "error", err)
		} else if len(generatedFiles) > 0 {
			for filename, content := range generatedFiles {
				s.QueueFile(instanceID, filename, content)
			}
		}
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

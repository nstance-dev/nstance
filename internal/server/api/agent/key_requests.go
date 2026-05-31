// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/config"
)

// ReceiveKeyRequests provides a persistent stream of key generation requests from server to agent
func (s *Service) ReceiveKeyRequests(req *emptypb.Empty, stream proto.AgentService_ReceiveKeyRequestsServer) error {
	clientInfo, err := api.GetClientInfo(stream.Context())
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	instanceID := clientInfo.ClientID
	s.logger.Info("Starting key generation request stream", "instance_id", instanceID)

	// Send any pending key requests immediately
	if err := s.sendPendingKeyRequests(instanceID, stream); err != nil {
		return err
	}

	// Keep stream open and wait for context cancellation (client disconnect)
	<-stream.Context().Done()
	s.logger.Info("Key generation request stream closed", "instance_id", instanceID)
	return nil
}

// sendPendingKeyRequests sends all pending key requests for an instance
func (s *Service) sendPendingKeyRequests(instanceID string, stream proto.AgentService_ReceiveKeyRequestsServer) error {
	pendingRequests := s.getPendingKeyRequests(instanceID)
	if len(pendingRequests) == 0 {
		s.logger.Debug("No pending key requests for instance", "instance_id", instanceID)
		return nil
	}

	s.logger.Info("Streaming pending key requests",
		"instance_id", instanceID,
		"request_count", len(pendingRequests))

	for _, request := range pendingRequests {
		keyRequest := &proto.KeyGenerationRequest{
			KeyNames: request.KeyNames,
		}

		if err := stream.Send(keyRequest); err != nil {
			s.logger.Error("Failed to stream key generation request",
				"instance_id", instanceID,
				"key_names", request.KeyNames,
				"error", err)
			return status.Errorf(codes.Internal, "failed to stream key generation request: %v", err)
		}

		s.logger.Debug("Streamed key generation request",
			"instance_id", instanceID,
			"key_names", request.KeyNames)
	}

	s.logger.Info("Key generation requests sent",
		"instance_id", instanceID,
		"requests_delivered", len(pendingRequests))

	return nil
}

// analyzeRequiredKeys examines the template configuration to determine which keys need to be generated
func (s *Service) analyzeRequiredKeys(templateConfig config.TemplateConfig) []string {
	var requiredKeys []string

	for _, fileConfig := range templateConfig.Files {
		// Only look at certificate files that require agent keys
		if fileConfig.Kind == "certificate" && fileConfig.Key != nil {
			// Check if the key source is from agent
			if fileConfig.Key.Source == "agent" {
				// Remove .pub extension if present to get the base key name
				baseKeyName := strings.TrimSuffix(fileConfig.Key.Name, ".pub")
				requiredKeys = append(requiredKeys, baseKeyName)
			}
		}
	}

	return requiredKeys
}

// getRequiredKeysForInstance returns the keys required by an instance's template
func (s *Service) getRequiredKeysForInstance(instanceID string) ([]string, error) {
	// Get instance record to find template
	instance, err := s.localDB.GetInstance(instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance record: %w", err)
	}

	// Get current configuration
	currentConfig := s.configLoader.GetCurrent()
	if currentConfig == nil {
		return nil, fmt.Errorf("no current config available")
	}

	// Derive template from instance group (merge static + dynamic groups)
	groups, err := config.GetGroups(context.Background(), s.configLoader, instance.Tenant)
	if err != nil {
		return nil, fmt.Errorf("failed to get groups: %w", err)
	}
	groupConfig, exists := groups[instance.Group]
	if !exists {
		return nil, fmt.Errorf("instance group not found: %s", instance.Group)
	}
	templateName := groupConfig.Template

	// Get template configuration
	templateConfig, exists := currentConfig.Templates[templateName]
	if !exists {
		return nil, fmt.Errorf("instance template not found: %s", templateName)
	}

	// Analyze template to determine required keys
	return s.analyzeRequiredKeys(templateConfig), nil
}

// checkAndRequestMissingKeys checks if an instance has any missing keys and sends key generation requests
func (s *Service) checkAndRequestMissingKeys(instanceID string) error {
	// Get required keys for this instance
	requiredKeys, err := s.getRequiredKeysForInstance(instanceID)
	if err != nil {
		return err
	}
	if len(requiredKeys) == 0 {
		return nil
	}

	// Check which keys are missing by querying the database
	missingKeys := []string{}
	for _, keyName := range requiredKeys {
		publicKey, err := s.localDB.GetPublicKeyByFilename(instanceID, keyName)
		if err != nil {
			s.logger.Error("Failed to check for public key",
				"instance_id", instanceID,
				"key_name", keyName,
				"error", err)
			continue
		}
		if publicKey == nil {
			missingKeys = append(missingKeys, keyName)
		}
	}

	// Queue key generation request for missing keys
	if len(missingKeys) > 0 {
		s.logger.Info("Queuing key generation request for missing keys",
			"instance_id", instanceID,
			"missing_keys", missingKeys)

		// Queue the key request for later retrieval by the agent
		s.queueKeyRequest(instanceID, missingKeys)
	}

	return nil
}

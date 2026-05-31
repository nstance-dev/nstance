// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

func (s *Service) RenewCertificate(ctx context.Context, req *proto.RenewCertificateRequest) (*proto.RenewCertificateResponse, error) {
	// Validate request
	if len(req.PublicKeyPem) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "public_key_pem is required")
	}

	// Check cluster leadership (consistent with RegisterOperator)
	if err := s.checkClusterLeadership(); err != nil {
		return nil, err
	}

	// Extract client info from authenticated mTLS context
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	// Log renewal request
	s.logger.Info("Processing certificate renewal request",
		"cluster_id", clientInfo.ClientID,
		"tenant", clientInfo.Tenant)

	// Verify this is an operator certificate
	if clientInfo.Role != "operator" {
		s.logger.Warn("Certificate renewal denied: invalid role",
			"cluster_id", clientInfo.ClientID,
			"role", clientInfo.Role)
		return nil, status.Errorf(codes.PermissionDenied, "certificate renewal only available for operators")
	}

	// Get current config to determine certificate TTL
	cfg := s.configLoader.GetCurrent()
	if cfg == nil {
		s.logger.Error("Certificate renewal failed: config not loaded")
		return nil, status.Errorf(codes.Internal, "config not loaded")
	}

	// Generate new client certificate using extracted identity
	ttl := s.getCertificateTTL("operator")
	certPEM, expiresAt, err := pki.GenerateClientCertificate(
		s.caCertPEM,
		s.caKeyPEM,
		req.PublicKeyPem,
		clientInfo.ClientID, // cluster ID from peer certificate
		clientInfo.Role,     // "operator"
		clientInfo.Tenant,   // tenant from peer certificate
		ttl,
	)
	if err != nil {
		s.logger.Error("Failed to generate renewed certificate",
			"cluster_id", clientInfo.ClientID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to generate certificate")
	}

	// Extract certificate serial for storage
	certSerial, err := pki.ExtractCertSerial(certPEM)
	if err != nil {
		s.logger.Error("Failed to extract certificate serial",
			"cluster_id", clientInfo.ClientID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to process certificate")
	}

	// Update registration record in S3 (cluster storage)
	if err := s.updateOperatorRegistrationInS3(ctx, clientInfo.ClientID, clientInfo.Tenant, req.PublicKeyPem, certSerial, expiresAt); err != nil {
		s.logger.Error("Failed to update registration record in S3",
			"cluster_id", clientInfo.ClientID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to update registration")
	}

	// Update SQLite cache
	if err := s.localDB.UpdateOperatorRegistration(clientInfo.ClientID, string(req.PublicKeyPem)); err != nil {
		s.logger.Warn("Failed to update registration cache in SQLite",
			"cluster_id", clientInfo.ClientID,
			"error", err)
		// Continue anyway - S3 is source of truth
	}

	s.logger.Info("Certificate renewed successfully",
		"cluster_id", clientInfo.ClientID,
		"expires_at", expiresAt)

	return &proto.RenewCertificateResponse{
		ClientCertificatePem: certPEM,
		ExpiresAt:            timestamppb.New(expiresAt),
	}, nil
}

// checkClusterLeadership returns an error if this instance is not the cluster leader
func (s *Service) checkClusterLeadership() error {
	if s.isClusterLeader == nil {
		s.logger.Error("Cluster leadership check unavailable")
		return status.Errorf(codes.Internal, "cluster leadership check not configured")
	}

	if !s.isClusterLeader() {
		return status.Errorf(codes.FailedPrecondition, "not the cluster leader")
	}
	return nil
}

// getCertificateTTL returns the TTL in hours for the given role
func (s *Service) getCertificateTTL(role string) int {
	cfg := s.configLoader.GetCurrent()
	if cfg == nil || cfg.Certificates == nil {
		return 24 * 365 // Default 1 year in hours
	}

	if cert, ok := cfg.Certificates[role]; ok && cert.TTL > 0 {
		return cert.TTL
	}
	return 24 * 365 // Default 1 year in hours
}

// updateOperatorRegistrationInS3 updates the operator registration record in S3
func (s *Service) updateOperatorRegistrationInS3(ctx context.Context, clusterID, tenant string, publicKeyPEM []byte, certSerial string, expiresAt time.Time) error {
	if s.clusterStorage == nil {
		return fmt.Errorf("cluster storage not configured")
	}

	// Create registration record
	record := map[string]interface{}{
		"cluster_id":  clusterID,
		"tenant":      tenant,
		"public_key":  string(publicKeyPEM),
		"cert_serial": certSerial,
		"expires_at":  expiresAt.UTC().Format(time.RFC3339),
		"renewed_at":  time.Now().UTC().Format(time.RFC3339),
		"type":        "operator",
	}

	// Encode as JSON
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to encode registration record: %w", err)
	}

	// Store in S3 at operator/{tenant}.{cluster_id}.json
	key := fmt.Sprintf("operator/%s.%s.json", tenant, clusterID)
	return s.clusterStorage.Put(ctx, key, data)
}

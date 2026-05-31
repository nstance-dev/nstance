// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

func (s *Service) RegisterOperator(ctx context.Context, req *proto.RegisterClientRequest) (*proto.RegisterClientResponse, error) {
	if err := s.checkClusterLeadership(); err != nil {
		return nil, err
	}

	s.logger.Info("Processing operator registration request")

	claims, err := s.jwtValidator.ValidateRegistrationNonce(req.RegistrationNonceJwt, "operator")
	if err != nil {
		s.logger.Warn("Operator registration failed: invalid JWT", "error", err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid registration nonce: %v", err)
	}

	cfg := s.configLoader.GetCurrent()
	if cfg == nil {
		s.logger.Error("Operator registration failed: config not loaded")
		return nil, status.Errorf(codes.Internal, "config not loaded")
	}

	if claims.ClusterID != cfg.Cluster.ID {
		s.logger.Warn("Operator registration failed: cluster_id mismatch",
			"expected", cfg.Cluster.ID, "got", claims.ClusterID)
		return nil, status.Errorf(codes.InvalidArgument, "cluster_id mismatch")
	}

	if err := s.localDB.ValidateOperatorNonce(req.RegistrationNonceJwt); err != nil {
		s.logger.Warn("Operator registration failed: nonce validation failed", "error", err)
		return nil, status.Errorf(codes.Unauthenticated, "nonce validation failed: %v", err)
	}

	clusterID := claims.Sub
	tenant := claims.Tenant

	ttl := s.getCertificateTTL("operator")
	certPEM, expiresAt, err := pki.GenerateClientCertificate(s.caCertPEM, s.caKeyPEM, req.PublicKeyPem, clusterID, "operator", tenant, ttl)
	if err != nil {
		s.logger.Error("Failed to generate operator certificate", "cluster_id", clusterID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to generate certificate")
	}

	certSerial, err := pki.ExtractCertSerial(certPEM)
	if err != nil {
		s.logger.Error("Failed to extract certificate serial", "cluster_id", clusterID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to process certificate")
	}

	if err := s.storeRegistrationInS3(ctx, clusterID, tenant, req.PublicKeyPem, certSerial, expiresAt, nil, "", "", ""); err != nil {
		s.logger.Error("Failed to store registration record in S3", "cluster_id", clusterID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to complete registration")
	}

	if err := s.localDB.CreateOperatorRegistrationRecord(clusterID, tenant, req.RegistrationNonceJwt, req.PublicKeyPem); err != nil {
		s.logger.Warn("Failed to cache registration record in SQLite", "cluster_id", clusterID, "error", err)
	}

	s.logger.Info("Operator registered successfully", "cluster_id", clusterID, "expires_at", expiresAt)

	return &proto.RegisterClientResponse{
		ClientCertificatePem: certPEM,
		ExpiresAt:            timestamppb.New(expiresAt),
	}, nil
}

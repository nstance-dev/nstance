// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
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

func (s *Service) RegisterAgent(ctx context.Context, req *proto.RegisterClientRequest) (*proto.RegisterClientResponse, error) {
	if err := s.checkShardLeadership(); err != nil {
		return nil, err
	}

	s.logger.Info("Processing agent registration request")

	claims, err := s.jwtValidator.ValidateRegistrationNonce(req.RegistrationNonceJwt, "agent")
	if err != nil {
		s.logger.Warn("Agent registration failed: invalid JWT", "error", err)
		return nil, status.Errorf(codes.Unauthenticated, "invalid registration nonce: %v", err)
	}

	cfg := s.configLoader.GetCurrent()
	if cfg == nil {
		s.logger.Error("Agent registration failed: config not loaded")
		return nil, status.Errorf(codes.Internal, "config not loaded")
	}

	if claims.ClusterID != cfg.Cluster.ID {
		s.logger.Warn("Agent registration failed: cluster_id mismatch",
			"expected", cfg.Cluster.ID, "got", claims.ClusterID)
		return nil, status.Errorf(codes.InvalidArgument, "cluster_id mismatch")
	}

	if claims.Shard != cfg.Shard.ID {
		s.logger.Warn("Agent registration failed: shard mismatch",
			"expected", cfg.Shard.ID, "got", claims.Shard)
		return nil, status.Errorf(codes.InvalidArgument, "shard mismatch")
	}

	instance, err := s.localDB.GetInstanceByNonce(req.RegistrationNonceJwt)
	if err != nil {
		s.logger.Warn("Agent registration failed: nonce validation failed", "error", err)
		return nil, status.Errorf(codes.Unauthenticated, "nonce validation failed: %v", err)
	}

	if instance.RegisteredAt != nil {
		s.logger.Warn("Agent registration failed: nonce already used", "instance_id", instance.ID)
		return nil, status.Errorf(codes.Unauthenticated, "nonce already used")
	}

	instanceID := claims.Sub
	tenant := claims.Tenant

	ttl := s.getCertificateTTL("agent")
	certPEM, expiresAt, err := pki.GenerateClientCertificate(s.caCertPEM, s.caKeyPEM, req.PublicKeyPem, instanceID, "agent", tenant, ttl)
	if err != nil {
		s.logger.Error("Failed to generate agent certificate", "instance_id", instanceID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to generate certificate")
	}

	certSerial, err := pki.ExtractCertSerial(certPEM)
	if err != nil {
		s.logger.Error("Failed to extract certificate serial", "instance_id", instanceID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to process certificate")
	}

	if err := s.storeRegistrationInS3(ctx, instanceID, tenant, req.PublicKeyPem, certSerial, expiresAt,
		instance.ProviderID, req.PrivateIpv4, req.PrivateIpv6, req.Hostname); err != nil {
		s.logger.Error("Failed to store registration record in S3", "instance_id", instanceID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to complete registration")
	}

	if err := s.localDB.MarkInstanceRegistered(instanceID, req.PublicKeyPem, req.PrivateIpv4, req.PrivateIpv6, req.Hostname); err != nil {
		s.logger.Warn("Failed to update SQLite with registration data", "instance_id", instanceID, "error", err)
	}

	s.logger.Info("Agent registered successfully", "instance_id", instanceID, "expires_at", expiresAt)

	return &proto.RegisterClientResponse{
		ClientCertificatePem: certPEM,
		ExpiresAt:            timestamppb.New(expiresAt),
	}, nil
}

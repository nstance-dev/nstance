// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/pki"
	"github.com/nstance-dev/nstance/internal/server/storage"
	"github.com/nstance-dev/nstance/pkg/nonce"
)

func (s *Service) RegisterOperator(ctx context.Context, req *proto.RegisterClientRequest) (*proto.RegisterClientResponse, error) {
	if err := s.checkClusterLeadership(); err != nil {
		return nil, err
	}

	s.logger.Info("Processing operator registration request")

	claims, err := nonce.Validate(req.RegistrationNonceJwt, s.noncePublicKey, "operator")
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
	if claims.Subject != claims.ClusterID {
		return nil, status.Errorf(codes.InvalidArgument, "subject and cluster_id mismatch")
	}

	clusterID := claims.Subject
	tenant := claims.Tenant

	ttl := s.getCertificateTTL("operator")
	certPEM, expiresAt, err := pki.GenerateClientCertificate(s.caCertPEM, s.caKeyPEM, req.PublicKeyPem, clusterID, "operator", tenant, ttl)
	if err != nil {
		s.logger.Error("Failed to generate operator certificate", "cluster_id", clusterID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to generate certificate")
	}

	firstUse, err := s.claimOperatorNonce(ctx, req.RegistrationNonceJwt, req.PublicKeyPem)
	if err != nil {
		s.logger.Warn("Operator registration failed: nonce validation failed", "error", err)
		return nil, status.Errorf(codes.Unauthenticated, "nonce validation failed: %v", err)
	}

	certSerial, err := pki.ExtractCertSerial(certPEM)
	if err != nil {
		s.logger.Error("Failed to extract certificate serial", "cluster_id", clusterID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to process certificate")
	}
	if firstUse {
		if err := s.localDB.CreateOperatorRegistrationRecord(clusterID, tenant, req.RegistrationNonceJwt, req.PublicKeyPem); err != nil {
			s.logger.Warn("Failed to cache operator registration binding", "cluster_id", clusterID, "error", err)
		}
	}

	if err := s.storeRegistrationInS3(ctx, clusterID, tenant, req.PublicKeyPem, certSerial, expiresAt, nil, "", "", ""); err != nil {
		s.logger.Error("Failed to store registration record in S3", "cluster_id", clusterID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to complete registration")
	}

	s.logger.Info("Operator registered successfully", "cluster_id", clusterID, "expires_at", expiresAt)

	return &proto.RegisterClientResponse{
		ClientCertificatePem: certPEM,
		ExpiresAt:            timestamppb.New(expiresAt),
	}, nil
}

// claimOperatorNonce durably binds a nonce to the first public key that uses
// it. The conditional create makes retries safe across server and leader
// replacement while rejecting replay with different key material.
func (s *Service) claimOperatorNonce(ctx context.Context, nonce string, publicKeyPEM []byte) (bool, error) {
	digest := sha256.Sum256([]byte(nonce))
	key := "operator-nonce/" + hex.EncodeToString(digest[:]) + ".pem"
	if err := s.clusterStorage.PutIfMatch(ctx, key, publicKeyPEM, ""); err == nil {
		return true, nil
	} else if !errors.Is(err, storage.ErrPrecondition) {
		return false, fmt.Errorf("persist nonce binding: %w", err)
	}

	existing, _, err := s.clusterStorage.Get(ctx, key)
	if err != nil {
		return false, fmt.Errorf("read nonce binding: %w", err)
	}
	if !bytes.Equal(existing, publicKeyPEM) {
		return false, fmt.Errorf("nonce already used with a different public key")
	}
	return false, nil
}

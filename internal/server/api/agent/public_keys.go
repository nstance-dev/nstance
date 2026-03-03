// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/base64"
	"encoding/pem"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/localdb"
)

// SubmitPublicKeys processes public key submissions by storing them transactionally
func (s *Service) SubmitPublicKeys(ctx context.Context, req *proto.PublicKeysRequest) (*emptypb.Empty, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	// Verify the instance ID matches the authenticated client
	if clientInfo.ClientID != req.InstanceId {
		s.logger.Warn("Public keys instance ID mismatch",
			"authenticated_client", clientInfo.ClientID,
			"reported_instance", req.InstanceId)
		return nil, status.Errorf(codes.PermissionDenied, "instance ID mismatch")
	}

	s.logger.Info("Storing public key submissions",
		"instance_id", req.InstanceId,
		"key_count", len(req.Keys))

	// Convert and validate public keys
	dbKeys := s.convertPublicKeys(req.Keys)

	if err := s.localDB.StorePublicKeys(req.InstanceId, dbKeys); err != nil {
		s.logger.Error("Failed to cache public keys in SQLite",
			"instance_id", req.InstanceId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to cache public keys")
	}

	s.logger.Info("Public key submission stored successfully",
		"instance_id", req.InstanceId,
		"keys_stored", len(dbKeys))

	return &emptypb.Empty{}, nil
}

func (s *Service) convertPublicKeys(protoKeys []*proto.PublicKeySubmission) []*localdb.PublicKeySubmission {
	var dbKeys []*localdb.PublicKeySubmission

	for _, keySubmission := range protoKeys {
		publicKeyDER, err := base64.StdEncoding.DecodeString(string(keySubmission.PublicKeyPem))
		if err != nil {
			s.logger.Warn("Failed to decode public key, skipping",
				"filename", keySubmission.Filename,
				"error", err)
			continue
		}

		publicKeyPEM := convertDERToPEM(publicKeyDER)

		dbKeys = append(dbKeys, &localdb.PublicKeySubmission{
			Filename:     keySubmission.Filename,
			PublicKeyPEM: string(publicKeyPEM),
		})
	}

	return dbKeys
}

func convertDERToPEM(derData []byte) []byte {
	block := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: derData,
	}
	return pem.EncodeToMemory(block)
}

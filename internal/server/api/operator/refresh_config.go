// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
)

func (s *Service) RefreshConfig(ctx context.Context, req *emptypb.Empty) (*proto.RefreshConfigResponse, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	s.logger.Info("Refreshing configuration", "client_id", clientInfo.ClientID)

	oldMetadata := s.configLoader.GetMetadata()
	oldETag := ""
	if oldMetadata != nil {
		oldETag = oldMetadata.ETag
	}

	// Load with forceRefresh=true to bypass cache
	// This also loads dynamic groups and syncs to SQLite
	_, err = s.configLoader.LoadConfigAndGroups(ctx, true)
	if err != nil {
		s.logger.Error("Failed to refresh configuration", "client_id", clientInfo.ClientID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to refresh configuration: %v", err)
	}

	newMetadata := s.configLoader.GetMetadata()
	newETag := ""
	if newMetadata != nil {
		newETag = newMetadata.ETag
	}

	updated := oldETag != newETag
	if updated {
		s.logger.Info("Configuration updated", "client_id", clientInfo.ClientID, "old_etag", oldETag, "new_etag", newETag)

		// Trigger reconciliation for all groups across all tenants
		config := s.configLoader.GetCurrent()
		if config != nil {
			for tenant, tenantGroups := range config.Groups {
				for groupKey := range tenantGroups {
					s.onGroupChanged(tenant, groupKey)
				}
			}
		}
	} else {
		s.logger.Info("Configuration unchanged", "client_id", clientInfo.ClientID, "etag", newETag)
	}

	return &proto.RefreshConfigResponse{
		Updated:  updated,
		PrevEtag: oldETag,
		Etag:     newETag,
	}, nil
}

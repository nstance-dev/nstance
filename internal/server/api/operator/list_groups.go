// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"maps"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/config"
)

func (s *Service) ListGroups(ctx context.Context, req *emptypb.Empty) (*proto.ListGroupsResponse, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	tenant := clientInfo.Tenant
	s.logger.Info("Listing groups", "client_id", clientInfo.ClientID, "tenant", tenant)

	groups, err := s.listGroups(tenant)
	if err != nil {
		s.logger.Error("Failed to list groups", "client_id", clientInfo.ClientID, "tenant", tenant, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list groups: %v", err)
	}

	s.logger.Info("Listed groups successfully", "client_id", clientInfo.ClientID, "tenant", tenant, "count", len(groups))

	return &proto.ListGroupsResponse{
		Groups: groups,
	}, nil
}

func (s *Service) listGroups(tenant string) ([]*proto.GroupStatus, error) {
	dbGroups, err := s.localDB.GetAllGroups(tenant)
	if err != nil {
		return nil, err
	}

	cfg := s.configLoader.GetCurrent()
	if cfg == nil {
		return nil, fmt.Errorf("no configuration loaded")
	}

	// Get static groups for this tenant
	staticGroups := cfg.Groups[tenant]

	var groups []*proto.GroupStatus
	for groupKey := range dbGroups {
		var staticGroup config.GroupConfig
		var isStatic bool
		if staticGroups != nil {
			staticGroup, isStatic = staticGroups[groupKey]
		}
		dynamicGroup, hasDynamic := s.configLoader.GetDynamicGroup(tenant, groupKey)

		var finalGroup config.GroupConfig
		if isStatic {
			finalGroup = staticGroup
			if hasDynamic {
				finalGroup.Vars = maps.Clone(staticGroup.Vars)
				if dynamicGroup.Size != nil {
					finalGroup.Size = dynamicGroup.Size
				}
				if dynamicGroup.InstanceType != "" {
					finalGroup.InstanceType = dynamicGroup.InstanceType
				}
				if len(dynamicGroup.Vars) > 0 {
					if finalGroup.Vars == nil {
						finalGroup.Vars = make(map[string]string)
					}
					for k, v := range dynamicGroup.Vars {
						finalGroup.Vars[k] = v
					}
				}
			}
		} else if hasDynamic {
			finalGroup = dynamicGroup
		} else {
			s.logger.Warn("Group in database not found in current config", "group_key", groupKey)
			continue
		}

		groups = append(groups, s.buildGroupStatus(tenant, groupKey, finalGroup, isStatic))
	}

	return groups, nil
}

// buildGroupStatus constructs the operator-facing status for a tenant-scoped group.
func (s *Service) buildGroupStatus(tenant, groupKey string, group config.GroupConfig, isStatic bool) *proto.GroupStatus {
	status := &proto.GroupStatus{
		Key:          groupKey,
		Tenant:       tenant,
		Template:     group.Template,
		Size:         int32(group.GetSize()),
		InstanceType: group.InstanceType,
		SubnetPool:   group.SubnetPool,
		Vars:         group.Vars,
		IsStatic:     isStatic,
	}
	providerIDs, err := s.localDB.GetProviderIDsByGroup(tenant, groupKey, true)
	if err != nil {
		s.logger.Warn("Failed to get provider IDs for group", "tenant", tenant, "group", groupKey, "error", err)
	} else {
		status.ActualSize = int32(len(providerIDs))
		status.ProviderIds = providerIDs
	}
	status.Etag = computeGroupEtag(status)
	return status
}

// computeGroupEtag generates a deterministic hash of a group's merged config for change detection.
// Only configuration fields are included — runtime state (actual_size, provider_ids) is excluded
// so that instance lifecycle changes don't cause unnecessary config change signals.
func computeGroupEtag(g *proto.GroupStatus) string {
	data, err := json.Marshal(map[string]any{
		"key":           g.Key,
		"tenant":        g.Tenant,
		"template":      g.Template,
		"size":          g.Size,
		"instance_type": g.InstanceType,
		"subnet_pool":   g.SubnetPool,
		"vars":          g.Vars,
		"is_static":     g.IsStatic,
	})
	if err != nil {
		return ""
	}

	hash := md5.Sum(data)
	return fmt.Sprintf("%x", hash)
}

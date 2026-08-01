// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/localdb"
)

// RefreshConfig reloads the static shard configuration after an administrator uploads it.
// It compares the old and new effective group sets, synchronizes the local group cache,
// publishes group changes to connected operators, and requests reconciliation for affected
// groups. Dynamic groups are changed through the group APIs rather than force-refreshed here.
// The mutex serializes this old/new comparison with UpsertGroup and DeleteGroup so concurrent
// mutations cannot produce stale reconciliation work or group events in the wrong order.
func (s *Service) RefreshConfig(ctx context.Context, req *emptypb.Empty) (*proto.RefreshConfigResponse, error) {
	clientInfo, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	s.logger.Info("Refreshing configuration", "client_id", clientInfo.ClientID)

	s.groupMutationMu.Lock()
	defer s.groupMutationMu.Unlock()

	// Snapshot the effective groups before reloading the static configuration.
	oldGroups, err := config.GetAllGroups(ctx, s.configLoader)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list groups before refresh: %v", err)
	}
	oldStatuses := make(map[localdb.ManagedGroupIdentity]*proto.GroupStatus, len(oldGroups))
	identities := make(map[localdb.ManagedGroupIdentity]struct{}, len(oldGroups))
	for _, group := range oldGroups {
		identity := localdb.ManagedGroupIdentity{Tenant: group.Tenant, Group: group.Key}
		_, isStatic := s.configLoader.GetCurrent().Groups[group.Tenant][group.Key]
		oldStatuses[identity] = s.buildGroupStatus(group.Tenant, group.Key, group.Config, isStatic)
		identities[identity] = struct{}{}
	}

	oldMetadata := s.configLoader.GetMetadata()
	oldETag := ""
	if oldMetadata != nil {
		oldETag = oldMetadata.ETag
	}

	// Bypass the static configuration cache and synchronize the effective group cache.
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

	// Snapshot the new effective groups and include removed groups that still own instances.
	groups, err := config.GetAllGroups(ctx, s.configLoader)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list groups after refresh: %v", err)
	}
	newStatuses := make(map[localdb.ManagedGroupIdentity]*proto.GroupStatus, len(groups))
	for _, group := range groups {
		identity := localdb.ManagedGroupIdentity{Tenant: group.Tenant, Group: group.Key}
		_, isStatic := s.configLoader.GetCurrent().Groups[group.Tenant][group.Key]
		newStatuses[identity] = s.buildGroupStatus(group.Tenant, group.Key, group.Config, isStatic)
		identities[identity] = struct{}{}
	}
	managedGroups, err := s.localDB.GetActiveManagedGroupIdentities()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list active managed groups after refresh: %v", err)
	}
	for _, identity := range managedGroups {
		identities[identity] = struct{}{}
	}

	// Reconcile every group whose effective state may have changed.
	for identity := range identities {
		s.onGroupChanged(identity.Tenant, identity.Group)
	}

	// Publish deletions and updates before additions to describe the completed transition.
	groupsUpdated := false
	for identity, oldStatus := range oldStatuses {
		newStatus, exists := newStatuses[identity]
		if !exists {
			groupsUpdated = true
			s.NotifyGroupEvent(identity.Tenant, &proto.GroupEvent{
				Type:  proto.GroupEvent_TYPE_DELETE,
				Group: &proto.GroupStatus{Tenant: identity.Tenant, Key: identity.Group},
			})
			continue
		}
		if oldStatus.Etag != newStatus.Etag {
			groupsUpdated = true
			s.NotifyGroupEvent(identity.Tenant, &proto.GroupEvent{Type: proto.GroupEvent_TYPE_UPSERT, Group: newStatus})
		}
	}
	for identity, newStatus := range newStatuses {
		if _, exists := oldStatuses[identity]; exists {
			continue
		}
		groupsUpdated = true
		s.NotifyGroupEvent(identity.Tenant, &proto.GroupEvent{Type: proto.GroupEvent_TYPE_UPSERT, Group: newStatus})
	}

	// The response reports whether the static configuration object changed.
	updated := oldETag != newETag
	if updated {
		s.logger.Info("Configuration updated", "client_id", clientInfo.ClientID, "old_etag", oldETag, "new_etag", newETag, "groups_updated", groupsUpdated)
	} else {
		s.logger.Info("Configuration unchanged", "client_id", clientInfo.ClientID, "etag", newETag)
	}

	return &proto.RefreshConfigResponse{
		Updated:  updated,
		PrevEtag: oldETag,
		Etag:     newETag,
	}, nil
}

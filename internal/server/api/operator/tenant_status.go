// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
)

// GetTenantStatus returns sleep state and current listener activity for one tenant.
func (s *Service) GetTenantStatus(ctx context.Context, req *proto.GetTenantStatusRequest) (*proto.GetTenantStatusResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	tenant, err := s.authorizedTenant(ctx, req.GetTenant())
	if err != nil {
		return nil, err
	}
	if s.tenantState == nil || s.localDB == nil || s.configLoader == nil {
		return nil, status.Error(codes.FailedPrecondition, "tenant status is unavailable")
	}
	asleep, wakeAt := s.tenantState.Status(tenant)
	response := &proto.GetTenantStatusResponse{Status: proto.TenantSleepStatus_TENANT_SLEEP_STATUS_AWAKE}
	if asleep {
		response.Status = proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP
	}
	if wakeAt != nil {
		response.WakeAt = timestamppb.New(*wakeAt)
	}
	cfg := s.configLoader.GetCurrent()
	if cfg == nil {
		return nil, status.Error(codes.FailedPrecondition, "configuration is unavailable")
	}
	proxyConfig, err := cfg.ProxyConfig()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "derive proxy listeners: %v", err)
	}
	activity := s.listenerActivity.Snapshot(tenant)
	names := make([]string, 0)
	for name, listener := range proxyConfig.Listeners {
		if listener.Tenant != tenant {
			continue
		}
		for _, groupName := range listener.Groups {
			group := cfg.Groups[tenant][groupName]
			if group.Size != nil && *group.Size > 0 {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names)
	for _, name := range names {
		configured := proxyConfig.Listeners[name]
		available := true
		var idleSince time.Time
		instances := 0
		for _, groupName := range configured.Groups {
			group := cfg.Groups[tenant][groupName]
			if group.Size == nil || *group.Size <= 0 {
				continue
			}
			ids, err := s.localDB.GetInstancesByGroup(tenant, groupName, true)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "list listener instances: %v", err)
			}
			for _, id := range ids {
				instances++
				item, ok := activity[name][id]
				if !ok || !item.Available {
					available = false
					continue
				}
				if item.LastActive.After(idleSince) {
					idleSince = item.LastActive
				}
			}
		}
		available = available && instances > 0
		listener := &proto.ListenerActivity{Listener: name, Available: available}
		if available {
			listener.IdleSince = timestamppb.New(idleSince)
		}
		response.Listeners = append(response.Listeners, listener)
	}
	return response, nil
}

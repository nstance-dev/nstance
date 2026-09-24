// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package wake

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/pkg/proxy"
)

const defaultUpstreamPollInterval = 200 * time.Millisecond

// TenantWaker wakes a tenant and waits for its upstream under one tenant operation.
type TenantWaker interface {
	WakeAndWait(context.Context, string, func(context.Context) error) (bool, error)
}

// InstanceStore provides the local authoritative instance observations needed
// to select a freshly healthy private upstream.
type InstanceStore interface {
	GetInstancesByGroup(tenant, group string, excludeOnDemand bool) ([]string, error)
	GetInstance(instanceID string) (*localdb.Instance, error)
}

// ListenerHandler resolves listener identities and completes serialized wakes.
type ListenerHandler struct {
	config       func() (proxy.Config, error)
	state        TenantWaker
	instances    InstanceStore
	pollInterval time.Duration
	dial         func(context.Context, string) error
	now          func() time.Time
}

// NewListenerHandler creates the listener-scoped wake handler used by ProxyService.
func NewListenerHandler(config func() (proxy.Config, error), state TenantWaker, instances InstanceStore) (*ListenerHandler, error) {
	if config == nil || state == nil || instances == nil {
		return nil, fmt.Errorf("proxy config, tenant state, and instance store are required")
	}
	dialer := net.Dialer{}
	return &ListenerHandler{
		config:       config,
		state:        state,
		instances:    instances,
		pollInterval: defaultUpstreamPollInterval,
		dial: func(ctx context.Context, address string) error {
			connection, err := dialer.DialContext(ctx, "tcp", address)
			if err == nil {
				_ = connection.Close()
			}
			return err
		},
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// WakeListener resolves the listener from current authoritative configuration,
// wakes its tenant, and returns only a freshly healthy, TCP-ready private target.
func (h *ListenerHandler) WakeListener(ctx context.Context, identity string) (*proto.WakeTenantResponse, error) {
	if identity == "" {
		return nil, status.Error(codes.InvalidArgument, "listener is required")
	}
	cfg, err := h.config()
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "proxy configuration unavailable: %v", err)
	}
	listener, ok := cfg.Listeners[identity]
	if !ok {
		return nil, status.Error(codes.NotFound, "listener is not configured")
	}
	var upstream string
	alreadyAwake, err := h.state.WakeAndWait(ctx, listener.Tenant, func(ctx context.Context) error {
		value, err := h.waitForUpstream(ctx, listener, h.now())
		upstream = value
		return err
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, status.Error(codes.DeadlineExceeded, "timed out waiting for a ready upstream")
		}
		return nil, status.Errorf(codes.Unavailable, "listener wake failed: %v", err)
	}
	result := proto.WakeTenantResponse_RESULT_WOKE
	if alreadyAwake {
		result = proto.WakeTenantResponse_RESULT_ALREADY_AWAKE
	}
	return &proto.WakeTenantResponse{
		Result:   result,
		Status:   proto.TenantSleepStatus_TENANT_SLEEP_STATUS_AWAKE,
		Upstream: &upstream,
	}, nil
}

// waitForUpstream waits for a freshly healthy and reachable listener target.
func (h *ListenerHandler) waitForUpstream(ctx context.Context, listener proxy.Listener, freshAfter time.Time) (string, error) {
	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()
	for {
		for _, group := range listener.Groups {
			instanceIDs, err := h.instances.GetInstancesByGroup(listener.Tenant, group, true)
			if err != nil {
				return "", fmt.Errorf("list group %s instances: %w", group, err)
			}
			for _, instanceID := range instanceIDs {
				instance, err := h.instances.GetInstance(instanceID)
				if err != nil || instance.HealthAt == nil || instance.HealthAt.Before(freshAfter) || instance.IP4 == nil || net.ParseIP(*instance.IP4) == nil {
					continue
				}
				address := net.JoinHostPort(*instance.IP4, strconv.Itoa(listener.TargetPort))
				if err := h.dial(ctx, address); err == nil {
					return address, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

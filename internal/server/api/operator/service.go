// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	serverconfig "github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// DrainNotification represents a drain notification event
type DrainNotification struct {
	InstanceID  string
	Group       string
	Reason      string
	UnhealthyAt time.Time
	DeleteAt    time.Time
}

// groupsStream serializes snapshot and live group events on one operator stream.
type groupsStream struct {
	stream proto.OperatorService_WatchGroupsServer
	mu     sync.Mutex
}

// instancesStream serializes snapshot and live instance events on one operator stream.
type instancesStream struct {
	stream proto.OperatorService_WatchInstancesServer
	mu     sync.Mutex
}

// Service implements the OperatorService gRPC service
type Service struct {
	proto.UnimplementedOperatorServiceServer

	configLoader    *serverconfig.Loader
	localDB         *localdb.DB
	instanceManager InstanceManager
	onGroupChanged  func(tenant, groupKey string)
	onDrainAcked    func(tenant, instanceID string)
	logger          *slog.Logger

	// Certificate renewal dependencies
	clusterStorage  storage.Storage
	caCertPEM       []byte
	caKeyPEM        []byte
	isClusterLeader func() bool

	// Operator stream tracking
	groupMutationMu  sync.Mutex
	streamMu         sync.Mutex
	groupsStreams    map[string]*groupsStream
	instancesStreams map[string]*instancesStream
	errorsStreams    map[string]proto.OperatorService_WatchErrorsServer
}

// InstanceManager interface for instance operations
type InstanceManager interface {
	CreateInstance(ctx context.Context, req instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error)
	DeleteInstance(ctx context.Context, tenant, instanceID string) error
	GetInstanceStatus(ctx context.Context, tenant, instanceID string) (*instances.InstanceStatus, error)
	ValidateInstanceTenant(tenant, instanceID string) error
}

// ptrToString converts a string pointer to string (empty if nil)
func ptrToString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// tenantInstanceError maps instance ownership and lookup failures to operator API errors.
func tenantInstanceError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return status.Error(codes.NotFound, "instance not found")
	}
	if errors.Is(err, instances.ErrInstanceTenantMismatch) {
		return status.Error(codes.PermissionDenied, "instance belongs to another tenant")
	}
	return status.Errorf(codes.Internal, "failed to get instance: %v", err)
}

// Options contains options for creating an OperatorService
type Options struct {
	ConfigLoader    *serverconfig.Loader
	LocalDB         *localdb.DB
	InstanceManager InstanceManager
	OnGroupChanged  func(tenant, groupKey string)
	OnDrainAcked    func(tenant, instanceID string)
	Logger          *slog.Logger

	// Certificate renewal dependencies
	ClusterStorage  storage.Storage
	CACertPEM       []byte
	CAKeyPEM        []byte
	IsClusterLeader func() bool
}

// New creates a new OperatorService
func New(opts Options) (*Service, error) {
	if opts.ConfigLoader == nil {
		return nil, fmt.Errorf("config loader is required")
	}
	if opts.LocalDB == nil {
		return nil, fmt.Errorf("local database is required")
	}
	if opts.InstanceManager == nil {
		return nil, fmt.Errorf("instance manager is required")
	}
	if opts.OnGroupChanged == nil {
		return nil, fmt.Errorf("onGroupChanged callback is required")
	}
	if opts.OnDrainAcked == nil {
		return nil, fmt.Errorf("onDrainAcked callback is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Service{
		configLoader:    opts.ConfigLoader,
		localDB:         opts.LocalDB,
		instanceManager: opts.InstanceManager,
		onGroupChanged:  opts.OnGroupChanged,
		onDrainAcked:    opts.OnDrainAcked,
		logger:          opts.Logger,

		// Certificate renewal dependencies (optional for backward compatibility)
		clusterStorage:   opts.ClusterStorage,
		caCertPEM:        opts.CACertPEM,
		caKeyPEM:         opts.CAKeyPEM,
		isClusterLeader:  opts.IsClusterLeader,
		groupsStreams:    make(map[string]*groupsStream),
		instancesStreams: make(map[string]*instancesStream),
		errorsStreams:    make(map[string]proto.OperatorService_WatchErrorsServer),
	}, nil
}

// NotifyDrain sends a drain notification to the connected operator (if any)
func (s *Service) NotifyDrain(notification DrainNotification) {
	// Get instance to retrieve provider ID
	instance, err := s.localDB.GetInstance(notification.InstanceID)
	if err != nil {
		s.logger.Error("Failed to get instance for drain notification",
			"instance_id", notification.InstanceID,
			"error", err)
		return
	}

	// Validate provider instance ID is present
	if instance.ProviderID == nil || *instance.ProviderID == "" {
		s.logger.Error("Instance missing provider ID, cannot send drain notification",
			"instance_id", notification.InstanceID)
		return
	}
	providerInstanceID := *instance.ProviderID

	// Validate tenant is present
	if instance.Tenant == "" {
		s.logger.Error("Instance missing tenant, cannot send drain notification",
			"instance_id", notification.InstanceID)
		return
	}

	s.streamMu.Lock()
	stream := s.instancesStreams[instance.Tenant]
	s.streamMu.Unlock()

	if stream == nil {
		s.logger.Warn("No operator connected for tenant, drain notification skipped (instance will be deleted after timeout)",
			"tenant", instance.Tenant,
			"instance_id", notification.InstanceID,
			"delete_at", notification.DeleteAt)
		return
	}

	event := &proto.InstanceEvent{
		InstanceId:         notification.InstanceID,
		Tenant:             instance.Tenant,
		Group:              notification.Group,
		Status:             "pending_deletion",
		Reason:             notification.Reason,
		UnhealthyAt:        timestamppb.New(notification.UnhealthyAt),
		DeleteAt:           timestamppb.New(notification.DeleteAt),
		ProviderInstanceId: providerInstanceID,
	}

	stream.mu.Lock()
	err = stream.stream.Send(event)
	stream.mu.Unlock()
	if err != nil {
		s.logger.Warn("Failed to send drain notification", "instance_id", notification.InstanceID, "error", err)
	} else {
		s.logger.Info("Sent drain notification to operator", "instance_id", notification.InstanceID, "provider_instance_id", providerInstanceID)
	}
}

// NotifyGroupEvent sends a group change event to the tenant's connected operator (if any).
func (s *Service) NotifyGroupEvent(tenant string, event *proto.GroupEvent) {
	s.streamMu.Lock()
	stream := s.groupsStreams[tenant]
	s.streamMu.Unlock()

	if stream == nil {
		return
	}

	stream.mu.Lock()
	err := stream.stream.Send(event)
	stream.mu.Unlock()
	if err != nil {
		group := ""
		if event.Group != nil {
			group = event.Group.Key
		}
		s.logger.Warn("Failed to send group event", "tenant", tenant, "group", group, "error", err)
	}
}

// NotifyError sends an error event to the tenant's connected operator (if any).
func (s *Service) NotifyError(tenant string, event *proto.ErrorEvent) {
	s.streamMu.Lock()
	stream := s.errorsStreams[tenant]
	s.streamMu.Unlock()

	if stream == nil {
		s.logger.Debug("No operator connected, skipping error notification",
			"group", event.Group,
			"instance_id", event.InstanceId)
		return
	}

	if err := stream.Send(event); err != nil {
		s.logger.Warn("Failed to send error event",
			"group", event.Group,
			"instance_id", event.InstanceId,
			"error", err)
	}
}

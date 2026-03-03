// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

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

// Service implements the OperatorService gRPC service
type Service struct {
	proto.UnimplementedOperatorServiceServer

	configLoader    *serverconfig.Loader
	localDB         *localdb.DB
	instanceManager InstanceManager
	onGroupChanged  func(tenant, groupKey string)
	onDrainAcked    func(instanceID string)
	logger          *slog.Logger

	// Certificate renewal dependencies
	clusterStorage  storage.Storage
	caCertPEM       []byte
	caKeyPEM        []byte
	isClusterLeader func() bool

	// Operator stream tracking
	streamMu        sync.Mutex
	groupsStream    proto.OperatorService_WatchGroupsServer
	instancesStream proto.OperatorService_WatchInstancesServer
	errorsStream    proto.OperatorService_WatchErrorsServer
}

// InstanceManager interface for instance operations
type InstanceManager interface {
	CreateInstance(ctx context.Context, req instances.CreateInstanceRequest) (*instances.CreateInstanceResponse, error)
	DeleteInstance(ctx context.Context, instanceID string) error
	GetInstanceStatus(ctx context.Context, instanceID string) (*instances.InstanceStatus, error)
}

// ptrToString converts a string pointer to string (empty if nil)
func ptrToString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Options contains options for creating an OperatorService
type Options struct {
	ConfigLoader    *serverconfig.Loader
	LocalDB         *localdb.DB
	InstanceManager InstanceManager
	OnGroupChanged  func(tenant, groupKey string)
	OnDrainAcked    func(instanceID string)
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
		clusterStorage:  opts.ClusterStorage,
		caCertPEM:       opts.CACertPEM,
		caKeyPEM:        opts.CAKeyPEM,
		isClusterLeader: opts.IsClusterLeader,
	}, nil
}

// NotifyDrain sends a drain notification to the connected operator (if any)
func (s *Service) NotifyDrain(notification DrainNotification) {
	s.streamMu.Lock()
	stream := s.instancesStream
	s.streamMu.Unlock()

	if stream == nil {
		s.logger.Warn("No operator connected, drain notification skipped (instance will be deleted after timeout)",
			"instance_id", notification.InstanceID,
			"delete_at", notification.DeleteAt)
		return
	}

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

	if err := stream.Send(event); err != nil {
		s.logger.Warn("Failed to send drain notification", "instance_id", notification.InstanceID, "error", err)
	} else {
		s.logger.Info("Sent drain notification to operator", "instance_id", notification.InstanceID, "provider_instance_id", providerInstanceID)
	}
}

// NotifyGroupEvent sends a group change event to the connected operator (if any)
func (s *Service) NotifyGroupEvent(event *proto.GroupEvent) {
	s.streamMu.Lock()
	stream := s.groupsStream
	s.streamMu.Unlock()

	if stream == nil {
		return
	}

	if err := stream.Send(event); err != nil {
		s.logger.Warn("Failed to send group event", "group", event.Group.Key, "error", err)
	}
}

// NotifyError sends an error event to the connected operator (if any)
func (s *Service) NotifyError(event *proto.ErrorEvent) {
	s.streamMu.Lock()
	stream := s.errorsStream
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

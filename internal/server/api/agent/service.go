// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/filegen"
	"github.com/nstance-dev/nstance/internal/server/infra"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/pki"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// Service implements the AgentService gRPC service
type Service struct {
	proto.UnimplementedAgentServiceServer

	storage              storage.Storage
	configLoader         *config.Loader
	localDB              *localdb.DB
	secretsStore         secrets.Store
	fileGenerator        *filegen.Generator
	provider             infra.Provider
	logger               *slog.Logger
	onSpotTermination    func(instanceID string, notice *proto.TerminationNotice) error
	onReconcileRequested func(groupKey, reason string) error
	onInstanceDisconnect func(instanceID string, graceful bool) error

	// In-memory pending key requests with mutex for thread safety
	pendingKeyRequestsMu sync.RWMutex
	pendingKeyRequests   map[string][]*PendingKeyRequest // instanceID -> key requests

	// In-memory pending files with mutex for thread safety
	pendingFilesMu sync.RWMutex
	pendingFiles   map[string][]*PendingFile // instanceID -> files
}

// Options contains options for creating an AgentService
type Options struct {
	Storage              storage.Storage
	ConfigLoader         *config.Loader
	LocalDB              *localdb.DB
	SecretsStore         secrets.Store
	CACertPEM            []byte
	CAKeyPEM             []byte
	Shard                string
	Provider             infra.Provider
	Logger               *slog.Logger
	OnSpotTermination    func(instanceID string, notice *proto.TerminationNotice) error
	OnReconcileRequested func(groupKey, reason string) error
	OnInstanceDisconnect func(instanceID string, graceful bool) error
}

// New creates a new AgentService
func New(opts Options) (*Service, error) {
	if opts.Storage == nil {
		return nil, fmt.Errorf("storage is required")
	}
	if opts.ConfigLoader == nil {
		return nil, fmt.Errorf("config loader is required")
	}
	if opts.LocalDB == nil {
		return nil, fmt.Errorf("local database is required")
	}
	if opts.SecretsStore == nil {
		return nil, fmt.Errorf("secrets store is required")
	}
	if len(opts.CACertPEM) == 0 {
		return nil, fmt.Errorf("CA certificate PEM is required")
	}
	if len(opts.CAKeyPEM) == 0 {
		return nil, fmt.Errorf("CA key PEM is required")
	}
	if opts.Shard == "" {
		return nil, fmt.Errorf("shard is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	service := &Service{
		storage:              opts.Storage,
		configLoader:         opts.ConfigLoader,
		localDB:              opts.LocalDB,
		secretsStore:         opts.SecretsStore,
		provider:             opts.Provider,
		logger:               opts.Logger,
		onSpotTermination:    opts.OnSpotTermination,
		onReconcileRequested: opts.OnReconcileRequested,
		onInstanceDisconnect: opts.OnInstanceDisconnect,
		pendingFiles:         make(map[string][]*PendingFile),
		pendingKeyRequests:   make(map[string][]*PendingKeyRequest),
	}

	// Initialize certificate services
	if err := service.initializeCertificateServices(opts.CACertPEM, opts.CAKeyPEM, opts.Shard); err != nil {
		return nil, fmt.Errorf("failed to initialize certificate services: %w", err)
	}

	return service, nil
}

// initializeCertificateServices sets up the certificate generation pipeline
func (s *Service) initializeCertificateServices(caCertPEM, caKeyPEM []byte, shard string) error {
	// Create serial logger
	serialLogger := pki.NewS3SerialLogger(s.storage, shard)

	// Create batch certificate service
	certService := pki.NewBatchCertificateGenerator(caCertPEM, caKeyPEM, serialLogger)

	// Create file generator
	s.fileGenerator = filegen.NewGenerator(
		s.configLoader,
		s.localDB,
		certService,
		s.secretsStore,
		s.storage,
		s.logger,
	)

	return nil
}

// PendingKeyRequest represents a key generation request waiting to be delivered to an agent
type PendingKeyRequest struct {
	KeyNames []string
	Created  time.Time
}

// PendingFile represents a file waiting to be delivered to an agent
type PendingFile struct {
	Filename     string
	Content      []byte
	LastModified time.Time
}

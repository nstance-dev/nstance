// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/refreshjs/puidv7"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/keys"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// Service implements the RegistrationService gRPC service
type Service struct {
	proto.UnimplementedRegistrationServiceServer

	shardStorage    storage.Storage
	clusterStorage  storage.Storage
	secretsStore    secrets.Store
	localDB         *localdb.DB
	instanceManager *instances.Manager
	configLoader    *config.Loader
	jwtValidator    *api.JWTValidator
	caCertPEM       []byte
	caKeyPEM        []byte
	isShardLeader   func() bool
	isClusterLeader func() bool
	logger          *slog.Logger
}

// Options contains options for creating a RegistrationService
type Options struct {
	ShardStorage    storage.Storage // For shard-scoped writes (agent registration)
	ClusterStorage  storage.Storage // For cluster-scoped writes (operator registration)
	SecretsStore    secrets.Store
	LocalDB         *localdb.DB
	InstanceManager *instances.Manager
	ConfigLoader    *config.Loader
	IsShardLeader   func() bool // For agent registration
	IsClusterLeader func() bool // For operator registration
	Logger          *slog.Logger
}

// New creates a new RegistrationService
func New(opts Options) (*Service, error) {
	if opts.ShardStorage == nil {
		return nil, fmt.Errorf("shard storage is required")
	}
	if opts.ClusterStorage == nil {
		return nil, fmt.Errorf("cluster storage is required")
	}
	if opts.SecretsStore == nil {
		return nil, fmt.Errorf("secrets store is required")
	}
	if opts.LocalDB == nil {
		return nil, fmt.Errorf("local database is required")
	}
	if opts.ConfigLoader == nil {
		return nil, fmt.Errorf("config loader is required")
	}
	if opts.IsShardLeader == nil {
		return nil, fmt.Errorf("isShardLeader function is required")
	}
	if opts.IsClusterLeader == nil {
		return nil, fmt.Errorf("isClusterLeader function is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	service := &Service{
		shardStorage:    opts.ShardStorage,
		clusterStorage:  opts.ClusterStorage,
		secretsStore:    opts.SecretsStore,
		localDB:         opts.LocalDB,
		instanceManager: opts.InstanceManager,
		configLoader:    opts.ConfigLoader,
		isShardLeader:   opts.IsShardLeader,
		isClusterLeader: opts.IsClusterLeader,
		logger:          opts.Logger,
	}

	if err := service.initialize(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to initialize registration service: %w", err)
	}

	return service, nil
}

// checkShardLeadership returns an error if this instance is not the shard leader
func (s *Service) checkShardLeadership() error {
	if !s.isShardLeader() {
		return status.Errorf(codes.FailedPrecondition, "not the shard leader")
	}
	return nil
}

// checkClusterLeadership returns an error if this instance is not the cluster leader
func (s *Service) checkClusterLeadership() error {
	if !s.isClusterLeader() {
		return status.Errorf(codes.FailedPrecondition, "not the cluster leader")
	}
	return nil
}

// initialize loads secrets and sets up JWT validation and certificate generation
func (s *Service) initialize(ctx context.Context) error {
	nonceKeyData, err := s.secretsStore.Get(ctx, "registration-nonce.key")
	if err != nil {
		return fmt.Errorf("failed to load registration nonce key: %w", err)
	}

	noncePrivateKey, err := keys.ParseEd25519PrivateKey(nonceKeyData)
	if err != nil {
		return fmt.Errorf("failed to parse registration nonce private key: %w", err)
	}

	noncePublicKey := noncePrivateKey.Public().(ed25519.PublicKey)
	s.jwtValidator = api.NewJWTValidator(noncePublicKey)

	caCertData, _, err := s.clusterStorage.Get(ctx, "ca.crt")
	if err != nil {
		return fmt.Errorf("failed to load CA certificate from S3: %w", err)
	}

	caKeyData, err := s.secretsStore.Get(ctx, "ca.key")
	if err != nil {
		return fmt.Errorf("failed to load CA private key: %w", err)
	}

	s.caCertPEM = caCertData
	s.caKeyPEM = caKeyData

	return nil
}

// storeRegistrationInS3 creates a registration record in S3 for critical data persistence.
// For instances, this delegates to the instance manager. For operators, writes directly to S3.
// S3 path format for operators: operator/{tenant}.{storage-key}.json
func (s *Service) storeRegistrationInS3(ctx context.Context, clientID, tenant string, publicKeyPEM []byte, certSerial string, expiresAt time.Time, providerID *string, privateIPv4, privateIPv6, hostname string) error {
	if s.isInstanceID(clientID) {
		if s.instanceManager != nil {
			return s.instanceManager.UpdateRegistration(ctx, tenant, clientID, string(publicKeyPEM), certSerial, expiresAt, providerID, privateIPv4, privateIPv6, hostname)
		}
		return nil
	}

	if tenant == "" {
		return fmt.Errorf("tenant is required for operator registration")
	}

	storageKey, err := instances.StorageKey(clientID)
	if err != nil {
		storageKey = clientID
	}

	record := map[string]interface{}{
		"client_id":      clientID,
		"tenant":         tenant,
		"public_key_pem": string(publicKeyPEM),
		"cert_serial":    certSerial,
		"registered_at":  time.Now().UTC(),
		"expires_at":     expiresAt.UTC(),
	}

	recordData, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal registration record: %w", err)
	}

	key := fmt.Sprintf("operator/%s.%s.json", tenant, storageKey)
	return s.clusterStorage.Put(ctx, key, recordData)
}

// isInstanceID determines if the given ID is an instance ID (vs cluster ID)
func (s *Service) isInstanceID(id string) bool {
	_, err := puidv7.Decode(id, "")
	return err == nil
}

// getCertificateTTL returns the TTL in hours for the given certificate type (operator/agent)
func (s *Service) getCertificateTTL(certType string) int {
	cfg := s.configLoader.GetCurrent()
	if cfg == nil || cfg.Certificates == nil {
		return 24 // default 24 hours
	}

	if cert, ok := cfg.Certificates[certType]; ok && cert.TTL > 0 {
		return cert.TTL
	}
	return 24 // default 24 hours
}

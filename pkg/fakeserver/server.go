// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"google.golang.org/grpc"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

// Server is a lightweight fake Nstance server for test use. Callers configure
// tenant-scoped runtime options, prepare instance-scoped user-data values, then
// start the server so real nstance-agent processes can register, submit keys,
// and receive generated or static files.
type Server struct {
	cfg Config

	mu                   sync.Mutex
	open                 bool
	started              bool
	stopping             bool
	caCertPEM            []byte
	caKeyPEM             []byte
	nonceKeyPEM          []byte
	nonceKey             ed25519.PrivateKey
	serverCert           []byte
	serverKey            []byte
	registrationListener net.Listener
	agentListener        net.Listener
	registrationServer   *grpc.Server
	agentServer          *grpc.Server
	stopCh               chan struct{}
}

// New returns a fake server.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if cfg.ClusterID == "" {
		cfg.ClusterID = "local"
	}
	if cfg.ShardID == "" {
		cfg.ShardID = "local"
	}
	if cfg.ListenAddr != "" {
		if cfg.RegistrationListenAddr == "" {
			cfg.RegistrationListenAddr = cfg.ListenAddr
		}
		if cfg.AgentListenAddr == "" {
			cfg.AgentListenAddr = "127.0.0.1:0"
		}
	}
	if cfg.RegistrationListenAddr == "" {
		cfg.RegistrationListenAddr = "127.0.0.1:0"
	}
	if cfg.AgentListenAddr == "" {
		cfg.AgentListenAddr = "127.0.0.1:0"
	}
	if cfg.AdvertiseHost == "" {
		cfg.AdvertiseHost = "127.0.0.1"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{cfg: cfg, stopCh: make(chan struct{})}, nil
}

// Open initializes durable fake server state.
func (s *Server) Open(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open {
		return nil
	}
	var err error
	s.caCertPEM, s.caKeyPEM, err = s.loadOrCreateCA(ctx)
	if err != nil {
		return err
	}
	s.nonceKeyPEM, s.nonceKey, err = s.loadOrCreateNonceKey(ctx)
	if err != nil {
		return err
	}
	s.serverCert, s.serverKey, err = pki.GenerateServerCertificate(s.caCertPEM, s.caKeyPEM, s.cfg.RegistrationListenAddr, s.cfg.AgentListenAddr, s.cfg.AdvertiseHost)
	if err != nil {
		return fmt.Errorf("generate server certificate: %w", err)
	}
	s.open = true
	return nil
}

// Start starts registration and agent gRPC services.
func (s *Server) Start(ctx context.Context) error {
	if err := s.Open(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopping {
		return nil
	}
	s.stopCh = make(chan struct{})
	reg, err := net.Listen("tcp", s.cfg.RegistrationListenAddr)
	if err != nil {
		return fmt.Errorf("listen registration: %w", err)
	}
	agent, err := net.Listen("tcp", s.cfg.AgentListenAddr)
	if err != nil {
		if closeErr := reg.Close(); closeErr != nil {
			s.cfg.Logger.Warn("failed to close fake server registration listener", "error", closeErr)
		}
		return fmt.Errorf("listen agent: %w", err)
	}
	regSvc := &registrationService{s: s}
	agentSvc := &agentService{s: s}
	regSrv, agentSrv, err := s.grpcServers()
	if err != nil {
		if closeErr := reg.Close(); closeErr != nil {
			s.cfg.Logger.Warn("failed to close fake server registration listener", "error", closeErr)
		}
		if closeErr := agent.Close(); closeErr != nil {
			s.cfg.Logger.Warn("failed to close fake server agent listener", "error", closeErr)
		}
		return err
	}
	s.registrationListener, s.agentListener = reg, agent
	s.registrationServer, s.agentServer = regSrv, agentSrv
	proto.RegisterRegistrationServiceServer(regSrv, regSvc)
	proto.RegisterAgentServiceServer(agentSrv, agentSvc)
	go serveGRPC(s.cfg.Logger, "registration", regSrv, reg)
	go serveGRPC(s.cfg.Logger, "agent", agentSrv, agent)
	s.started = true
	return nil
}

// Stop stops the fake server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	if s.stopping {
		s.mu.Unlock()
		return nil
	}
	registrationServer := s.registrationServer
	agentServer := s.agentServer
	s.stopping = true
	close(s.stopCh)
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		if registrationServer != nil {
			registrationServer.GracefulStop()
		}
		if agentServer != nil {
			agentServer.GracefulStop()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		if registrationServer != nil {
			registrationServer.Stop()
		}
		if agentServer != nil {
			agentServer.Stop()
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = false
	s.stopping = false
	s.registrationListener = nil
	s.agentListener = nil
	s.registrationServer = nil
	s.agentServer = nil
	return nil
}

// Addr returns advertised registration and agent addresses.
func (s *Server) Addr() (registrationAddr, agentAddr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.registrationListener == nil || s.agentListener == nil {
		return "", ""
	}
	return s.advertiseAddr(s.registrationListener), s.advertiseAddr(s.agentListener)
}

// ConfigureTenant stores the runtime file and certificate config for a tenant.
func (s *Server) ConfigureTenant(ctx context.Context, cfg TenantConfig) error {
	if cfg.TenantID == "" {
		return fmt.Errorf("tenant id is required")
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.cfg.Store.Put(ctx, s.tenantKey(cfg.TenantID), data)
}

// ConfigureInstance stores instance state. The tenant referenced by req.TenantID
// must already have been configured via ConfigureTenant so the registration
// nonce can embed the tenant's runtime config hash, matching production behavior
func (s *Server) ConfigureInstance(ctx context.Context, req InstanceRequest) error {
	if err := s.Open(ctx); err != nil {
		return err
	}
	if req.TenantID == "" || req.InstanceID == "" {
		return fmt.Errorf("tenant id and instance id are required")
	}
	tenant, err := s.tenant(ctx, req.TenantID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("tenant %q must be configured via ConfigureTenant before ConfigureInstance", req.TenantID)
		}
		return fmt.Errorf("load tenant %q: %w", req.TenantID, err)
	}
	jwt, err := s.registrationJWT(req, tenant)
	if err != nil {
		return err
	}
	inst := persistedInstance{TenantID: req.TenantID, InstanceID: req.InstanceID, InstanceKind: req.InstanceKind, Hostname: req.Hostname, IPv4: req.IPv4, IPv6: req.IPv6, NonceJWT: jwt}
	return s.putInstance(ctx, &inst)
}

// CACert returns the fake server's CA certificate in PEM form. The server is
// opened on first call so callers can render tenant configuration that embeds
// the CA before configuring tenants or instances.
func (s *Server) CACert(ctx context.Context) ([]byte, error) {
	if err := s.Open(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.caCertPEM))
	copy(out, s.caCertPEM)
	return out, nil
}

// InstanceEnv returns environment variables for a configured instance nstance-agent.
func (s *Server) InstanceEnv(ctx context.Context, instanceID string) (InstanceEnv, error) {
	reg, agent := s.Addr()
	if reg == "" || agent == "" {
		return InstanceEnv{}, fmt.Errorf("server must be started before rendering instance env")
	}
	return s.InstanceEnvWithAddrs(ctx, instanceID, ServerAddrs{RegistrationAddr: reg, AgentAddr: agent})
}

// InstanceEnvWithAddrs returns environment variables for a configured instance
// nstance-agent using caller-provided server addresses.
func (s *Server) InstanceEnvWithAddrs(ctx context.Context, instanceID string, serverAddrs ServerAddrs) (InstanceEnv, error) {
	if err := s.Open(ctx); err != nil {
		return InstanceEnv{}, err
	}
	if serverAddrs.RegistrationAddr == "" || serverAddrs.AgentAddr == "" {
		return InstanceEnv{}, fmt.Errorf("registration and agent addresses are required")
	}
	if instanceID == "" {
		return InstanceEnv{}, fmt.Errorf("instance id is required")
	}
	inst, err := s.getInstance(ctx, instanceID)
	if err != nil {
		return InstanceEnv{}, err
	}
	return InstanceEnv{Vars: map[string]string{"NSTANCE_CA_CERT": base64.StdEncoding.EncodeToString(s.caCertPEM), "NSTANCE_REGISTRATION_NONCE_JWT": inst.NonceJWT, "NSTANCE_SERVER_REGISTRATION_ADDR": serverAddrs.RegistrationAddr, "NSTANCE_SERVER_AGENT_ADDR": serverAddrs.AgentAddr}}, nil
}

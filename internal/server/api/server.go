// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/reconciler"
)

// Server manages the three gRPC servers and reconciler for Nstance services
type Server struct {
	config *config.Config
	logger *slog.Logger
	debug  bool

	// TLS certificates
	caCert     *x509.Certificate
	serverCert tls.Certificate

	// gRPC servers
	registrationServer *grpc.Server
	agentServer        *grpc.Server
	operatorServer     *grpc.Server

	// Service implementations
	registrationService proto.RegistrationServiceServer
	agentService        proto.AgentServiceServer
	operatorService     proto.OperatorServiceServer

	// Reconciler
	reconciler *reconciler.Reconciler

	// Listeners
	RegistrationListener net.Listener
	AgentListener        net.Listener
	OperatorListener     net.Listener

	// Control
	mu       sync.RWMutex
	started  bool
	stopping bool
}

// ServerOptions contains options for creating a new Server
type ServerOptions struct {
	Config              *config.Config
	Logger              *slog.Logger
	RegistrationService proto.RegistrationServiceServer
	AgentService        proto.AgentServiceServer
	OperatorService     proto.OperatorServiceServer
	Reconciler          *reconciler.Reconciler
	CACertPEM           []byte // CA certificate PEM for client authentication
	ServerCertPEM       []byte // Server certificate PEM for TLS
	ServerKeyPEM        []byte // Server private key PEM for TLS
	Debug               bool   // Enable debug features (gRPC reflection)
}

// NewServer creates a new gRPC server manager
func NewServer(opts ServerOptions) (*Server, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("config is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.RegistrationService == nil {
		return nil, fmt.Errorf("registration service implementation is required")
	}
	if opts.AgentService == nil {
		return nil, fmt.Errorf("agent service implementation is required")
	}
	if opts.OperatorService == nil {
		return nil, fmt.Errorf("operator service implementation is required")
	}

	// Parse TLS certificates
	var caCert *x509.Certificate
	var serverCert tls.Certificate
	var err error

	if len(opts.CACertPEM) > 0 {
		caCert, err = ParseCertificateFromPEM(opts.CACertPEM)
		if err != nil {
			return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
		}
	}

	if len(opts.ServerCertPEM) > 0 && len(opts.ServerKeyPEM) > 0 {
		serverCert, err = tls.X509KeyPair(opts.ServerCertPEM, opts.ServerKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("failed to parse server certificate: %w", err)
		}
	}

	return &Server{
		config:              opts.Config,
		logger:              opts.Logger,
		debug:               opts.Debug,
		caCert:              caCert,
		serverCert:          serverCert,
		registrationService: opts.RegistrationService,
		agentService:        opts.AgentService,
		operatorService:     opts.OperatorService,
		reconciler:          opts.Reconciler,
	}, nil
}

// Start starts all three gRPC servers
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		s.logger.Debug("Server already started")
		return nil
	}

	s.logger.Info("Starting Nstance gRPC servers")

	// Create listeners
	if err := s.createListeners(); err != nil {
		return fmt.Errorf("failed to create listeners: %w", err)
	}

	// Create gRPC servers
	if err := s.createGRPCServers(); err != nil {
		return fmt.Errorf("failed to create gRPC servers: %w", err)
	}

	// Register services
	s.registerServices()

	// Start servers
	if err := s.startServers(ctx); err != nil {
		s.cleanup()
		return fmt.Errorf("failed to start servers: %w", err)
	}

	// Start reconciler
	// Note: Initial reconciliation is triggered by the OnBecomeLeader callback in root.go,
	// not here, to avoid duplicate events when becoming shard leader.
	if s.reconciler != nil {
		if err := s.reconciler.Start(ctx); err != nil {
			s.cleanup()
			return fmt.Errorf("failed to start reconciler: %w", err)
		}
		s.logger.Info("Reconciler started")
	}

	s.started = true
	s.logger.Info("All gRPC servers and reconciler started successfully",
		"registration", s.config.Shard.Bind.RegistrationAddr,
		"agent", s.config.Shard.Bind.AgentAddr,
		"operator", s.config.Shard.Bind.OperatorAddr)

	return nil
}

// Stop gracefully stops all gRPC servers
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started || s.stopping {
		s.logger.Debug("Server not started or already stopping")
		return nil
	}

	s.stopping = true
	s.logger.Info("Stopping Nstance gRPC servers and reconciler")

	// Stop reconciler first
	if s.reconciler != nil {
		s.reconciler.Stop()
		s.logger.Info("Reconciler stopped")
	}

	// Use a channel to coordinate graceful shutdown
	done := make(chan struct{})
	go func() {
		defer close(done)

		// Graceful stop with timeout
		if s.registrationServer != nil {
			s.registrationServer.GracefulStop()
		}
		if s.agentServer != nil {
			s.agentServer.GracefulStop()
		}
		if s.operatorServer != nil {
			s.operatorServer.GracefulStop()
		}
	}()

	// Wait for graceful shutdown or timeout
	select {
	case <-done:
		s.logger.Info("All gRPC servers stopped gracefully")
	case <-ctx.Done():
		s.logger.Warn("Graceful shutdown timeout, forcing stop")
		s.forceStop()
	}

	s.logger.Info("All gRPC servers and reconciler stopped")

	s.cleanup()
	s.started = false
	s.stopping = false

	return nil
}

// createListeners creates network listeners for all three services
func (s *Server) createListeners() error {
	var err error

	// Registration service listener
	regAddr := s.config.Shard.Bind.RegistrationAddr
	s.RegistrationListener, err = net.Listen("tcp", regAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on registration address %s: %w", regAddr, err)
	}

	// Agent service listener
	agentAddr := s.config.Shard.Bind.AgentAddr
	s.AgentListener, err = net.Listen("tcp", agentAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on agent address %s: %w", agentAddr, err)
	}

	// Operator service listener
	opAddr := s.config.Shard.Bind.OperatorAddr
	s.OperatorListener, err = net.Listen("tcp", opAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on operator address %s: %w", opAddr, err)
	}

	return nil
}

// createGRPCServers creates the gRPC server instances
func (s *Server) createGRPCServers() error {
	var err error

	// Check we have required certificates
	if s.caCert == nil || s.serverCert.Certificate == nil {
		return fmt.Errorf("required certificates not found")
	}

	// Create TLS credentials for registration server (server auth only, no client cert)
	var registrationCreds credentials.TransportCredentials
	if s.serverCert.Certificate != nil {
		// Configure TLS with server authentication only
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{s.serverCert},
			ClientAuth:   tls.NoClientCert,
			MinVersion:   tls.VersionTLS13,
		}
		registrationCreds = credentials.NewTLS(tlsConfig)
	}

	// Registration server (no authentication required, but TLS enabled)
	s.registrationServer = grpc.NewServer(
		grpc.MaxRecvMsgSize(4*1024*1024), // 4MB max message size
		grpc.MaxSendMsgSize(4*1024*1024),
		grpc.ConnectionTimeout(30*time.Second),
		grpc.Creds(registrationCreds),
	)

	// Create authentication interceptors for agent and operator services
	var agentAuth, operatorAuth *AuthInterceptor
	agentAuth, err = NewAuthInterceptor(s.caCert, "agent", s.logger)
	if err != nil {
		return fmt.Errorf("failed to create agent auth interceptor: %w", err)
	}
	operatorAuth, err = NewAuthInterceptor(s.caCert, "operator", s.logger)
	if err != nil {
		return fmt.Errorf("failed to create operator auth interceptor: %w", err)
	}

	// Create TLS credentials for agent and operator servers (mutual auth)
	var agentOperatorCreds credentials.TransportCredentials
	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(s.caCert)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{s.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS13,
	}
	agentOperatorCreds = credentials.NewTLS(tlsConfig)

	// Agent server (with authentication middleware, mutual TLS, and keepalive)
	s.agentServer = grpc.NewServer(
		grpc.MaxRecvMsgSize(4*1024*1024),
		grpc.MaxSendMsgSize(4*1024*1024),
		grpc.ConnectionTimeout(30*time.Second),
		grpc.UnaryInterceptor(agentAuth.UnaryServerInterceptor()),
		grpc.StreamInterceptor(agentAuth.StreamServerInterceptor()),
		grpc.Creds(agentOperatorCreds),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    20 * time.Second, // Send ping if no activity for 20s
			Timeout: 10 * time.Second, // Wait 10s for ping ack before closing connection
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second, // Min time between client pings
			PermitWithoutStream: true,             // Allow pings even without active RPCs
		}),
	)

	// Operator server (with authentication middleware and mutual TLS)
	s.operatorServer = grpc.NewServer(
		grpc.MaxRecvMsgSize(4*1024*1024),
		grpc.MaxSendMsgSize(4*1024*1024),
		grpc.ConnectionTimeout(30*time.Second),
		grpc.UnaryInterceptor(operatorAuth.UnaryServerInterceptor()),
		grpc.StreamInterceptor(operatorAuth.StreamServerInterceptor()),
		grpc.Creds(agentOperatorCreds),
	)

	// Enable reflection for development/debugging
	if s.debug {
		s.logger.Info("gRPC reflection enabled (debug mode)")
		reflection.Register(s.registrationServer)
		reflection.Register(s.agentServer)
		reflection.Register(s.operatorServer)
	}

	return nil
}

// registerServices registers the service implementations with gRPC servers
func (s *Server) registerServices() {
	// Register registration service
	proto.RegisterRegistrationServiceServer(s.registrationServer, s.registrationService)

	// Register agent service
	proto.RegisterAgentServiceServer(s.agentServer, s.agentService)

	// Register operator service
	proto.RegisterOperatorServiceServer(s.operatorServer, s.operatorService)
}

// startServers starts all gRPC servers in separate goroutines
func (s *Server) startServers(ctx context.Context) error {
	errChan := make(chan error, 3)

	// Start registration server
	go func() {
		s.logger.Info("Starting registration service", "address", s.config.Shard.Bind.RegistrationAddr)
		if err := s.registrationServer.Serve(s.RegistrationListener); err != nil {
			errChan <- fmt.Errorf("registration server failed: %w", err)
		}
	}()

	// Start agent server
	go func() {
		s.logger.Info("Starting agent service", "address", s.config.Shard.Bind.AgentAddr)
		if err := s.agentServer.Serve(s.AgentListener); err != nil {
			errChan <- fmt.Errorf("agent server failed: %w", err)
		}
	}()

	// Start operator server
	go func() {
		s.logger.Info("Starting operator service", "address", s.config.Shard.Bind.OperatorAddr)
		if err := s.operatorServer.Serve(s.OperatorListener); err != nil {
			errChan <- fmt.Errorf("operator server failed: %w", err)
		}
	}()

	// Give servers a moment to start
	select {
	case err := <-errChan:
		return err
	case <-time.After(100 * time.Millisecond):
		// Servers started successfully
		// Drain errChan to avoid blocking on late sends
		go func() {
			for range errChan {
			}
		}()
		return nil
	}
}

// forceStop forcefully stops all servers
func (s *Server) forceStop() {
	if s.registrationServer != nil {
		s.registrationServer.Stop()
	}
	if s.agentServer != nil {
		s.agentServer.Stop()
	}
	if s.operatorServer != nil {
		s.operatorServer.Stop()
	}
}

// cleanup closes listeners and resets server state
func (s *Server) cleanup() {
	if s.RegistrationListener != nil {
		err := s.RegistrationListener.Close()
		if err != nil {
			s.logger.Error("Failed to close registration listener", "err", err)
		}
		s.RegistrationListener = nil
	}
	if s.AgentListener != nil {
		err := s.AgentListener.Close()
		if err != nil {
			s.logger.Error("Failed to close agent listener", "err", err)
		}
		s.AgentListener = nil
	}
	if s.OperatorListener != nil {
		err := s.OperatorListener.Close()
		if err != nil {
			s.logger.Error("Failed to close operator listener", "err", err)
		}
		s.OperatorListener = nil
	}

	s.registrationServer = nil
	s.agentServer = nil
	s.operatorServer = nil
}

// IsStarted returns whether the server is currently started
func (s *Server) IsStarted() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.started
}

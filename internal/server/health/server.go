// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
)

// Server provides a simple HTTP health check endpoint for ASG health checks
type Server struct {
	bindAddr string
	logger   *slog.Logger
	server   *http.Server
	ready    atomic.Bool
}

// Config contains configuration for the health server
type Config struct {
	BindAddr string
	Logger   *slog.Logger
}

// NewServer creates a new HTTP health server
func NewServer(cfg Config) (*Server, error) {
	if cfg.BindAddr == "" {
		return nil, fmt.Errorf("bind address is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	s := &Server{
		bindAddr: cfg.BindAddr,
		logger:   cfg.Logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/", s.handleHealth) // Default route also serves health

	s.server = &http.Server{
		Addr:    cfg.BindAddr,
		Handler: mux,
	}

	return s, nil
}

// Start starts the HTTP health server
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("Starting HTTP health server", "addr", s.bindAddr)

	ln, err := net.Listen("tcp", s.bindAddr)
	if err != nil {
		return fmt.Errorf("failed to bind health server: %w", err)
	}

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP health server error", "error", err)
		}
	}()

	return nil
}

// Stop gracefully stops the HTTP health server
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Stopping HTTP health server")
	return s.server.Shutdown(ctx)
}

// SetReady marks the server as ready (returns 200 OK)
func (s *Server) SetReady() {
	s.ready.Store(true)
	s.logger.Info("Health endpoint marked as ready")
}

// SetNotReady marks the server as not ready (returns 503)
func (s *Server) SetNotReady() {
	s.ready.Store(false)
	s.logger.Info("Health endpoint marked as not ready")
}

// handleHealth handles HTTP health check requests
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.ready.Load() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("Service Unavailable\n"))
	}
}

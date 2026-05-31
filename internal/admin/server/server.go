// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/nstance-dev/nstance/internal/admin/service"
)

// Server is the HTTP API server for nstance-admin.
type Server struct {
	httpServer    *http.Server
	configService *service.ConfigService
	groupService  *service.GroupService
	logger        *slog.Logger
}

// Config contains configuration for the HTTP server.
type Config struct {
	BindAddr      string
	ConfigService *service.ConfigService
	GroupService  *service.GroupService
	Logger        *slog.Logger
}

// New creates a new HTTP API server.
func New(cfg Config) (*Server, error) {
	if cfg.BindAddr == "" {
		return nil, fmt.Errorf("bind address is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	s := &Server{
		configService: cfg.ConfigService,
		groupService:  cfg.GroupService,
		logger:        cfg.Logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /config/status", s.handleConfigStatus)
	mux.HandleFunc("POST /config/refresh", s.handleConfigRefresh)
	mux.HandleFunc("POST /group/scale", s.handleGroupScale)

	s.httpServer = &http.Server{
		Addr:              cfg.BindAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s, nil
}

// Start starts the HTTP server.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	s.logger.Info("listening", "addr", ln.Addr().String())

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", "error", err)
		}
	}()

	return nil
}

// Stop gracefully stops the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("stopping HTTP server")
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, errorResponse{Error: message})
}

type errorResponse struct {
	Error string `json:"error"`
}

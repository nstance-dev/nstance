// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package election

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"

	"github.com/nadrama-com/s3lect"
)

// HealthServerConfig contains configuration for the election health server.
type HealthServerConfig struct {
	// BindAddr is the address to listen on (e.g. ":8443")
	BindAddr string
	// TLSCert is the TLS certificate for the HTTPS server
	TLSCert tls.Certificate
}

// healthServer serves election health endpoints over HTTPS.
type healthServer struct {
	server *http.Server
	logger interface{ Info(string, ...any) }
}

// StartHealthServer creates and starts the election health HTTPS server.
// It registers handlers for /health/leadership/cluster and /health/leadership/shard.
// Unstarted electors will return 404 until their election is started.
func (m *Manager) StartHealthServer(ctx context.Context, cfg HealthServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.healthServer != nil {
		return fmt.Errorf("health server already started")
	}

	mux := http.NewServeMux()

	// Cluster election handler — returns 404 if cluster elector not yet started
	mux.HandleFunc("/health/leadership/cluster", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		elector := m.clusterElector
		m.mu.RUnlock()

		if elector == nil {
			http.NotFound(w, r)
			return
		}
		s3lect.NewLeadershipHandler(elector, m.cfg.Logger)(w, r)
	})

	// Shard election handler — returns 404 if shard elector not yet started
	mux.HandleFunc("/health/leadership/shard", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		elector := m.shardElector
		m.mu.RUnlock()

		if elector == nil {
			http.NotFound(w, r)
			return
		}
		s3lect.NewLeadershipHandler(elector, m.cfg.Logger)(w, r)
	})

	server := &http.Server{
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cfg.TLSCert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	ln, err := net.Listen("tcp", cfg.BindAddr)
	if err != nil {
		return fmt.Errorf("failed to bind election health server: %w", err)
	}

	tlsListener := tls.NewListener(ln, server.TLSConfig)

	go func() {
		if err := server.Serve(tlsListener); err != nil && err != http.ErrServerClosed {
			m.cfg.Logger.Error("Election health server error", "error", err)
		}
	}()

	m.healthServer = &healthServer{
		server: server,
		logger: m.cfg.Logger,
	}

	m.cfg.Logger.Info("Election health server started", "addr", cfg.BindAddr)
	return nil
}

// Stop gracefully shuts down the health server.
func (hs *healthServer) Stop(ctx context.Context) error {
	return hs.server.Shutdown(ctx)
}

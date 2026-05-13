// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/nstance-dev/nstance/internal/server/api"
)

// serveGRPC serves one fake-server gRPC endpoint until it is stopped.
func serveGRPC(logger *slog.Logger, name string, srv *grpc.Server, lis net.Listener) {
	if err := srv.Serve(lis); err != nil {
		logger.Debug("fake server stopped", "service", name, "error", err)
	}
}

// grpcServers constructs the registration and agent gRPC servers with their TLS settings.
func (s *Server) grpcServers() (*grpc.Server, *grpc.Server, error) {
	cert, err := tls.X509KeyPair(s.serverCert, s.serverKey)
	if err != nil {
		return nil, nil, fmt.Errorf("load server keypair: %w", err)
	}
	caCert, err := api.ParseCertificateFromPEM(s.caCertPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA certificate: %w", err)
	}
	regCreds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.NoClientCert,
		MinVersion:   tls.VersionTLS13,
	})
	regServer := grpc.NewServer(grpc.Creds(regCreds))

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	agentCreds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	})
	agentAuth, err := api.NewAuthInterceptor(caCert, "agent", s.cfg.Logger)
	if err != nil {
		return nil, nil, err
	}
	agentServer := grpc.NewServer(
		grpc.Creds(agentCreds),
		grpc.UnaryInterceptor(agentAuth.UnaryServerInterceptor()),
		grpc.StreamInterceptor(agentAuth.StreamServerInterceptor()),
	)
	return regServer, agentServer, nil
}

// advertiseAddr converts a listener address into the configured advertised host and listener port.
func (s *Server) advertiseAddr(l net.Listener) string {
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return net.JoinHostPort(s.cfg.AdvertiseHost, port)
}

// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"context"
	"crypto/ed25519"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

type registrationService struct {
	proto.UnimplementedRegistrationServiceServer
	s *Server
}

// RegisterAgent validates a prepared nonce and returns a client certificate for the agent.
func (r *registrationService) RegisterAgent(ctx context.Context, req *proto.RegisterClientRequest) (*proto.RegisterClientResponse, error) {
	validator := api.NewJWTValidator(r.s.nonceKey.Public().(ed25519.PublicKey))
	claims, err := validator.ValidateRegistrationNonce(req.RegistrationNonceJwt, "agent")
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid registration nonce: %v", err)
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	inst, err := r.s.getInstance(ctx, claims.Sub)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "nonce not found")
	}
	if inst.NonceJWT != req.RegistrationNonceJwt || inst.Registered {
		return nil, status.Errorf(codes.Unauthenticated, "nonce already used or unknown")
	}
	cert, exp, err := pki.GenerateClientCertificate(r.s.caCertPEM, r.s.caKeyPEM, req.PublicKeyPem, claims.Sub, "agent", claims.Tenant, 8760)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "generate certificate: %v", err)
	}
	inst.Registered = true
	if req.Hostname != "" {
		inst.Hostname = req.Hostname
	}
	if req.PrivateIpv4 != "" {
		inst.IPv4 = req.PrivateIpv4
	}
	if req.PrivateIpv6 != "" {
		inst.IPv6 = req.PrivateIpv6
	}
	if err := r.s.putInstance(ctx, inst); err != nil {
		return nil, status.Errorf(codes.Internal, "store registered instance: %v", err)
	}
	return &proto.RegisterClientResponse{ClientCertificatePem: cert, ExpiresAt: timestamppb.New(exp)}, nil
}

// RegisterOperator reports that operator registration is not implemented by the fake server.
func (r *registrationService) RegisterOperator(context.Context, *proto.RegisterClientRequest) (*proto.RegisterClientResponse, error) {
	return nil, status.Error(codes.Unimplemented, "operator registration is not implemented by fake server")
}

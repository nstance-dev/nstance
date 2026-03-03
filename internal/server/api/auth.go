// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// ClientInfo contains information about an authenticated client
type ClientInfo struct {
	ClientID string            // Instance ID or Cluster ID
	Role     string            // "agent" or "operator"
	Tenant   string            // Tenant identifier (from certificate Organization field)
	Cert     *x509.Certificate // Full client certificate
}

// ContextKey for storing client info in request context
type ContextKey string

const ClientInfoKey ContextKey = "client_info"

// AuthInterceptor provides gRPC interceptors for client certificate authentication
type AuthInterceptor struct {
	caCert       *x509.Certificate
	requiredRole string
	logger       *slog.Logger
}

// NewAuthInterceptor creates a new authentication interceptor
func NewAuthInterceptor(caCert *x509.Certificate, requiredRole string, logger *slog.Logger) (*AuthInterceptor, error) {
	if logger == nil {
		logger = slog.Default()
	}

	return &AuthInterceptor{
		caCert:       caCert,
		requiredRole: requiredRole,
		logger:       logger,
	}, nil
}

// UnaryServerInterceptor returns a unary server interceptor for authentication
func (a *AuthInterceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Authenticate and authorize the request
		newCtx, err := a.authenticate(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}

		// Call the handler with authenticated context
		return handler(newCtx, req)
	}
}

// StreamServerInterceptor returns a stream server interceptor for authentication
func (a *AuthInterceptor) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Authenticate and authorize the request
		newCtx, err := a.authenticate(ss.Context(), info.FullMethod)
		if err != nil {
			return err
		}

		// Create wrapped stream with authenticated context
		wrappedStream := &wrappedServerStream{
			ServerStream: ss,
			ctx:          newCtx,
		}

		// Call the handler with authenticated context
		return handler(srv, wrappedStream)
	}
}

// authenticate validates the client certificate and extracts client information
func (a *AuthInterceptor) authenticate(ctx context.Context, method string) (context.Context, error) {
	// Get peer information
	p, ok := peer.FromContext(ctx)
	if !ok {
		a.logger.Warn("Authentication failed: no peer info", "method", method)
		return nil, status.Errorf(codes.Unauthenticated, "no peer information")
	}

	// Extract TLS info
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		a.logger.Warn("Authentication failed: no TLS info", "method", method)
		return nil, status.Errorf(codes.Unauthenticated, "no TLS authentication info")
	}

	// Verify client provided certificate
	if len(tlsInfo.State.PeerCertificates) == 0 {
		a.logger.Warn("Authentication failed: no client certificate", "method", method)
		return nil, status.Errorf(codes.Unauthenticated, "no client certificate provided")
	}

	clientCert := tlsInfo.State.PeerCertificates[0]

	// Verify certificate chain
	if err := a.verifyCertificate(clientCert); err != nil {
		a.logger.Warn("Authentication failed: certificate verification failed", "method", method, "error", err)
		return nil, status.Errorf(codes.Unauthenticated, "certificate verification failed: %v", err)
	}

	// Extract client information
	clientInfo, err := a.extractClientInfo(clientCert)
	if err != nil {
		a.logger.Warn("Authentication failed: failed to extract client info", "method", method, "error", err)
		return nil, status.Errorf(codes.Unauthenticated, "failed to extract client info: %v", err)
	}

	// Authorize based on role
	if clientInfo.Role != a.requiredRole {
		a.logger.Warn("Authorization failed: insufficient role",
			"method", method,
			"client_id", clientInfo.ClientID,
			"required_role", a.requiredRole,
			"actual_role", clientInfo.Role)
		return nil, status.Errorf(codes.PermissionDenied, "insufficient permissions: required role %s, have role %s", a.requiredRole, clientInfo.Role)
	}

	a.logger.Debug("Authentication successful",
		"method", method,
		"client_id", clientInfo.ClientID,
		"role", clientInfo.Role)

	// Add client info to context
	return context.WithValue(ctx, ClientInfoKey, clientInfo), nil
}

// verifyCertificate verifies the client certificate against the CA
func (a *AuthInterceptor) verifyCertificate(clientCert *x509.Certificate) error {
	// Create certificate pool with our CA
	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(a.caCert)

	// Verify certificate
	opts := x509.VerifyOptions{
		Roots:     caCertPool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	_, err := clientCert.Verify(opts)
	if err != nil {
		return fmt.Errorf("certificate verification failed: %w", err)
	}

	return nil
}

// extractClientInfo extracts client ID, role, and tenant from the certificate
func (a *AuthInterceptor) extractClientInfo(cert *x509.Certificate) (*ClientInfo, error) {
	// Extract client ID from Common Name
	clientID := cert.Subject.CommonName
	if clientID == "" {
		return nil, fmt.Errorf("client ID not found in certificate")
	}

	// Extract tenant from Organization field
	// The server validates that certificates contain exactly one O value;
	// certificates with zero or multiple O values are rejected during authentication.
	// If no Organization, error
	var tenant string
	if len(cert.Subject.Organization) == 1 {
		tenant = cert.Subject.Organization[0]
	} else if len(cert.Subject.Organization) > 1 {
		return nil, fmt.Errorf("certificate contains multiple Organization values (expected exactly one for tenant)")
	} else {
		return nil, fmt.Errorf("certificate contains no Organization value (expected exactly one for tenant)")
	}

	// Extract role from custom extension
	var role string
	roleOID := []int{1, 3, 6, 1, 4, 1, 999999, 1} // Custom OID for role (matches certificate generation)

	for _, ext := range cert.Extensions {
		if ext.Id.Equal(roleOID) {
			var roleVal string
			if _, err := asn1.Unmarshal(ext.Value, &roleVal); err != nil {
				role = string(ext.Value)
			} else {
				role = roleVal
			}
			break
		}
	}

	if role == "" {
		return nil, fmt.Errorf("role not found in certificate")
	}

	return &ClientInfo{
		ClientID: clientID,
		Role:     role,
		Tenant:   tenant,
		Cert:     cert,
	}, nil
}

// GetClientInfo extracts client information from the request context
func GetClientInfo(ctx context.Context) (*ClientInfo, error) {
	value := ctx.Value(ClientInfoKey)
	if value == nil {
		return nil, fmt.Errorf("no client info in context")
	}

	clientInfo, ok := value.(*ClientInfo)
	if !ok {
		return nil, fmt.Errorf("invalid client info type in context")
	}

	return clientInfo, nil
}

// parseCertificateFromPEM parses a PEM-encoded certificate
func ParseCertificateFromPEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid PEM block type: %s", block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, nil
}

// wrappedServerStream wraps a grpc.ServerStream with a new context
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context returns the wrapped context
func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

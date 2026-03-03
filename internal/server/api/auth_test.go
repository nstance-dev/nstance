// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"log/slog"
	"testing"

	"github.com/nstance-dev/nstance/internal/server/keys"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

func TestAuthInterceptor(t *testing.T) {
	// Generate test CA
	caCertPEM, caPrivateKeyPEM, err := pki.GenerateTestCA()
	if err != nil {
		t.Fatalf("Failed to generate test CA: %v", err)
	}

	// Parse CA certificate
	caCert, err := ParseCertificateFromPEM(caCertPEM)
	if err != nil {
		t.Fatalf("Failed to parse CA certificate: %v", err)
	}

	t.Run("CreateAuthInterceptor", func(t *testing.T) {
		// Test creating auth interceptor
		auth, err := NewAuthInterceptor(caCert, "agent", slog.Default())
		if err != nil {
			t.Fatalf("Failed to create auth interceptor: %v", err)
		}

		if auth.requiredRole != "agent" {
			t.Errorf("Expected required role 'agent', got '%s'", auth.requiredRole)
		}
	})

	t.Run("ExtractClientInfo", func(t *testing.T) {
		// Generate test certificate with role
		agentPublicKeyPEM, err := keys.GenerateTestEd25519Key()
		if err != nil {
			t.Fatalf("Failed to generate agent key: %v", err)
		}

		agentCertPEM, _, err := pki.GenerateClientCertificate(caCertPEM, caPrivateKeyPEM, agentPublicKeyPEM, "test-agent", "agent", "default", 24)
		if err != nil {
			t.Fatalf("Failed to generate agent certificate: %v", err)
		}

		// Parse the certificate
		cert, err := ParseCertificateFromPEM(agentCertPEM)
		if err != nil {
			t.Fatalf("Failed to parse certificate: %v", err)
		}

		// Create auth interceptor and test client info extraction
		auth, err := NewAuthInterceptor(caCert, "agent", slog.Default())
		if err != nil {
			t.Fatalf("Failed to create auth interceptor: %v", err)
		}

		clientInfo, err := auth.extractClientInfo(cert)
		if err != nil {
			t.Fatalf("Failed to extract client info: %v", err)
		}

		if clientInfo.ClientID != "test-agent" {
			t.Errorf("Expected client ID 'test-agent', got '%s'", clientInfo.ClientID)
		}
		if clientInfo.Role != "agent" {
			t.Errorf("Expected role 'agent', got '%s'", clientInfo.Role)
		}
		if clientInfo.Tenant != "default" {
			t.Errorf("Expected tenant 'default', got '%s'", clientInfo.Tenant)
		}
	})

	t.Run("ExtractClientInfoWithTenant", func(t *testing.T) {
		// Generate test certificate with custom tenant
		agentPublicKeyPEM, err := keys.GenerateTestEd25519Key()
		if err != nil {
			t.Fatalf("Failed to generate agent key: %v", err)
		}

		agentCertPEM, _, err := pki.GenerateClientCertificate(caCertPEM, caPrivateKeyPEM, agentPublicKeyPEM, "test-agent", "agent", "prod", 24)
		if err != nil {
			t.Fatalf("Failed to generate agent certificate: %v", err)
		}

		// Parse the certificate
		cert, err := ParseCertificateFromPEM(agentCertPEM)
		if err != nil {
			t.Fatalf("Failed to parse certificate: %v", err)
		}

		// Create auth interceptor and test client info extraction
		auth, err := NewAuthInterceptor(caCert, "agent", slog.Default())
		if err != nil {
			t.Fatalf("Failed to create auth interceptor: %v", err)
		}

		clientInfo, err := auth.extractClientInfo(cert)
		if err != nil {
			t.Fatalf("Failed to extract client info: %v", err)
		}

		if clientInfo.Tenant != "prod" {
			t.Errorf("Expected tenant 'prod', got '%s'", clientInfo.Tenant)
		}
		if clientInfo.Tenant != "prod" {
			t.Errorf("Expected tenant 'prod', got '%s'", clientInfo.Tenant)
		}
	})

	t.Run("VerifyCertificate", func(t *testing.T) {
		// Generate valid agent certificate
		agentPublicKeyPEM, err := keys.GenerateTestEd25519Key()
		if err != nil {
			t.Fatalf("Failed to generate agent key: %v", err)
		}

		agentCertPEM, _, err := pki.GenerateClientCertificate(caCertPEM, caPrivateKeyPEM, agentPublicKeyPEM, "test-agent", "agent", "default", 24)
		if err != nil {
			t.Fatalf("Failed to generate agent certificate: %v", err)
		}

		// Parse the certificate
		cert, err := ParseCertificateFromPEM(agentCertPEM)
		if err != nil {
			t.Fatalf("Failed to parse certificate: %v", err)
		}

		// Create auth interceptor and test certificate verification
		auth, err := NewAuthInterceptor(caCert, "agent", slog.Default())
		if err != nil {
			t.Fatalf("Failed to create auth interceptor: %v", err)
		}

		// Should succeed
		err = auth.verifyCertificate(cert)
		if err != nil {
			t.Errorf("Certificate verification should succeed: %v", err)
		}
	})
}

func TestGetClientInfo(t *testing.T) {
	t.Run("ValidClientInfo", func(t *testing.T) {
		clientInfo := &ClientInfo{
			ClientID: "test-client",
			Role:     "agent",
		}

		ctx := context.WithValue(context.Background(), ClientInfoKey, clientInfo)

		retrieved, err := GetClientInfo(ctx)
		if err != nil {
			t.Fatalf("Failed to get client info: %v", err)
		}

		if retrieved.ClientID != "test-client" {
			t.Errorf("Expected client ID 'test-client', got '%s'", retrieved.ClientID)
		}
		if retrieved.Role != "agent" {
			t.Errorf("Expected role 'agent', got '%s'", retrieved.Role)
		}
	})

	t.Run("NoClientInfo", func(t *testing.T) {
		ctx := context.Background()

		_, err := GetClientInfo(ctx)
		if err == nil {
			t.Error("Expected error when no client info in context")
		}
	})

	t.Run("InvalidClientInfo", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ClientInfoKey, "invalid")

		_, err := GetClientInfo(ctx)
		if err == nil {
			t.Error("Expected error when invalid client info in context")
		}
	})
}

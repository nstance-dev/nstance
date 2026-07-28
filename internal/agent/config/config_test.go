// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadAppliesDefaults(t *testing.T) {
	// Create temporary directories for testing
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "identity")
	keysDir := filepath.Join(tmpDir, "keys")
	recvDir := filepath.Join(tmpDir, "recv")

	if err := os.MkdirAll(identityDir, 0755); err != nil {
		t.Fatalf("Failed to create identity dir: %v", err)
	}
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		t.Fatalf("Failed to create keys dir: %v", err)
	}
	if err := os.MkdirAll(recvDir, 0755); err != nil {
		t.Fatalf("Failed to create recv dir: %v", err)
	}

	t.Setenv("NSTANCE_SERVER_REGISTRATION_ADDR", "example.com:8992")
	t.Setenv("NSTANCE_SERVER_AGENT_ADDR", "example.com:8994")
	t.Setenv("NSTANCE_INSTANCE_ID", "knc0000000001r010000000000000")
	t.Setenv("NSTANCE_REPORT_INTERVAL", "")
	t.Setenv("NSTANCE_IDENTITY_MODE", "")
	t.Setenv("NSTANCE_KEYS_MODE", "")
	t.Setenv("NSTANCE_RECV_MODE", "")
	t.Setenv("NSTANCE_DEBUG", "")
	t.Setenv("NSTANCE_IDENTITY_DIR", identityDir)
	t.Setenv("NSTANCE_KEYS_DIR", keysDir)
	t.Setenv("NSTANCE_RECV_DIR", recvDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Debug {
		t.Errorf("Debug default = true, want false")
	}
	if cfg.Environment != "development" {
		t.Errorf("Environment = %q, want development", cfg.Environment)
	}
	if cfg.ReportInterval != 60*time.Second {
		t.Errorf("ReportInterval = %v, want 60s", cfg.ReportInterval)
	}

	if cfg.IdentityDir != identityDir {
		t.Errorf("Identity dir = %q, want %q", cfg.IdentityDir, identityDir)
	}
	if cfg.KeysDir != keysDir {
		t.Errorf("Keys dir = %q, want %q", cfg.KeysDir, keysDir)
	}
	if cfg.RecvDir != recvDir {
		t.Errorf("Recv dir = %q, want %q", cfg.RecvDir, recvDir)
	}
	if cfg.IdentityMode != "0600" {
		t.Errorf("IdentityMode = %s, want 0600", cfg.IdentityMode)
	}
	if cfg.KeysMode != "0640" {
		t.Errorf("KeysMode = %s, want 0640", cfg.KeysMode)
	}
	if cfg.RecvMode != "0640" {
		t.Errorf("RecvMode = %s, want 0640", cfg.RecvMode)
	}
	expectedRegAddr := "example.com:8992"
	if cfg.RegistrationAddr != expectedRegAddr {
		t.Errorf("RegistrationAddr = %q, want %q", cfg.RegistrationAddr, expectedRegAddr)
	}

	expectedAgentAddr := "example.com:8994"
	if cfg.AgentAddr != expectedAgentAddr {
		t.Errorf("AgentAddr = %q, want %q", cfg.AgentAddr, expectedAgentAddr)
	}
}

func TestLoadOverrides(t *testing.T) {
	// Create temporary directories for testing
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "identity")
	keysDir := filepath.Join(tmpDir, "keys")
	recvDir := filepath.Join(tmpDir, "recv")

	if err := os.MkdirAll(identityDir, 0755); err != nil {
		t.Fatalf("Failed to create identity dir: %v", err)
	}
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		t.Fatalf("Failed to create keys dir: %v", err)
	}
	if err := os.MkdirAll(recvDir, 0755); err != nil {
		t.Fatalf("Failed to create recv dir: %v", err)
	}

	t.Setenv("NSTANCE_SERVER_REGISTRATION_ADDR", "nstance-server.local:8992")
	t.Setenv("NSTANCE_SERVER_AGENT_ADDR", "nstance-server.local:8994")
	t.Setenv("NSTANCE_DEBUG", "true")
	t.Setenv("NSTANCE_ENVIRONMENT", "production")
	t.Setenv("NSTANCE_IDENTITY_DIR", identityDir)
	t.Setenv("NSTANCE_KEYS_DIR", keysDir)
	t.Setenv("NSTANCE_RECV_DIR", recvDir)
	t.Setenv("NSTANCE_INSTANCE_ID", "knc0000000001r010000000000001")
	t.Setenv("NSTANCE_IDENTITY_MODE", "0640")
	t.Setenv("NSTANCE_KEYS_MODE", "0640")
	t.Setenv("NSTANCE_RECV_MODE", "0660")
	t.Setenv("NSTANCE_INSTANCE_HOSTNAME", "agent-host")
	t.Setenv("NSTANCE_INSTANCE_FQDN", "agent.example.com")
	t.Setenv("NSTANCE_INSTANCE_IPV4", "192.0.2.1")
	t.Setenv("NSTANCE_INSTANCE_IPV6", "2001:db8::1")
	t.Setenv("NSTANCE_REPORT_INTERVAL", "0")
	t.Setenv("NSTANCE_METRICS_INTERFACE", "eth0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !cfg.Debug {
		t.Errorf("Debug = false, want true")
	}
	if cfg.Environment != "production" {
		t.Errorf("Environment = %q, want production", cfg.Environment)
	}
	if cfg.ReportInterval != 0 {
		t.Errorf("ReportInterval = %v, want 0", cfg.ReportInterval)
	}
	if cfg.MetricsInterface != "eth0" {
		t.Errorf("MetricsInterface = %q, want eth0", cfg.MetricsInterface)
	}

	if cfg.IdentityDir != identityDir {
		t.Errorf("Identity dir = %q, want %q", cfg.IdentityDir, identityDir)
	}
	if cfg.KeysDir != keysDir {
		t.Errorf("Keys dir = %q, want %q", cfg.KeysDir, keysDir)
	}
	if cfg.RecvDir != recvDir {
		t.Errorf("Recv dir = %q, want %q", cfg.RecvDir, recvDir)
	}

	if cfg.IdentityMode != "0640" {
		t.Errorf("IdentityMode = %s, want 0640", cfg.IdentityMode)
	}
	if cfg.KeysMode != "0640" {
		t.Errorf("KeysMode = %s, want 0640", cfg.KeysMode)
	}
	if cfg.RecvMode != "0660" {
		t.Errorf("RecvMode = %s, want 0660", cfg.RecvMode)
	}

	if cfg.InstanceHostname != "agent-host" {
		t.Errorf("InstanceHostname = %q, want agent-host", cfg.InstanceHostname)
	}
	if cfg.InstanceFQDN != "agent.example.com" {
		t.Errorf("InstanceFQDN = %q, want agent.example.com", cfg.InstanceFQDN)
	}
	if cfg.InstanceIPv4 != "192.0.2.1" {
		t.Errorf("InstanceIPv4 = %q, want 192.0.2.1", cfg.InstanceIPv4)
	}
	if cfg.InstanceIPv6 != "2001:db8::1" {
		t.Errorf("InstanceIPv6 = %q, want 2001:db8::1", cfg.InstanceIPv6)
	}

	expectedRegAddr := "nstance-server.local:8992"
	if cfg.RegistrationAddr != expectedRegAddr {
		t.Errorf("RegistrationAddr = %q, want %q", cfg.RegistrationAddr, expectedRegAddr)
	}

	expectedAgentAddr := "nstance-server.local:8994"
	if cfg.AgentAddr != expectedAgentAddr {
		t.Errorf("AgentAddr = %q, want %q", cfg.AgentAddr, expectedAgentAddr)
	}
}

func TestLoadInvalidValue(t *testing.T) {
	// Create temporary directories for testing
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "identity")
	keysDir := filepath.Join(tmpDir, "keys")
	recvDir := filepath.Join(tmpDir, "recv")

	if err := os.MkdirAll(identityDir, 0755); err != nil {
		t.Fatalf("Failed to create identity dir: %v", err)
	}
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		t.Fatalf("Failed to create keys dir: %v", err)
	}
	if err := os.MkdirAll(recvDir, 0755); err != nil {
		t.Fatalf("Failed to create recv dir: %v", err)
	}

	t.Setenv("NSTANCE_SERVER_REGISTRATION_ADDR", "nstance-server.local:8992")
	t.Setenv("NSTANCE_SERVER_AGENT_ADDR", "nstance-server.local:8994")
	t.Setenv("NSTANCE_IDENTITY_DIR", identityDir)
	t.Setenv("NSTANCE_KEYS_DIR", keysDir)
	t.Setenv("NSTANCE_RECV_DIR", recvDir)
	t.Setenv("NSTANCE_INSTANCE_ID", "knc0000000001r010000000000002")
	t.Setenv("NSTANCE_REPORT_INTERVAL", "invalid")

	_, err := Load()
	if err == nil {
		t.Fatalf("Load() error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "ReportInterval") || !strings.Contains(err.Error(), "invalid duration") {
		t.Errorf("error = %q, want duration parsing error", err)
	}
}

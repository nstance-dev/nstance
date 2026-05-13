// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"context"
	"testing"
	"time"
)

// TestConfigureTenantAndInstanceEnv verifies tenant and instance setup returns agent environment variables.
func TestConfigureTenantAndInstanceEnv(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	server, err := New(Config{Store: store, ClusterID: "podplane-local", ShardID: "local"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := server.ConfigureTenant(ctx, TenantConfig{
		TenantID: "cluster-a",
		Files: map[string]FileConfig{
			"kubelet.client.crt": {Kind: "certificate", Template: "kubelet.client", Key: &KeyConfig{Source: "agent", Name: "kubelet.client"}},
		},
		Certificates: map[string]CertificateConfig{
			"kubelet.client": {Kind: "client"},
		},
	}); err != nil {
		t.Fatalf("ConfigureTenant: %v", err)
	}
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Stop(stopCtx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	})
	if err := server.ConfigureInstance(ctx, InstanceRequest{TenantID: "cluster-a", InstanceID: "knc123", InstanceKind: "knc"}); err != nil {
		t.Fatalf("ConfigureInstance: %v", err)
	}
	instanceEnv, err := server.InstanceEnv(ctx, "knc123")
	if err != nil {
		t.Fatalf("InstanceEnv: %v", err)
	}
	tenant, err := server.tenant(ctx, "cluster-a")
	if err != nil {
		t.Fatalf("tenant: %v", err)
	}
	if tenant.Certificates["kubelet.client"].Kind != "client" {
		t.Fatalf("unexpected tenant certificate config: %#v", tenant.Certificates["kubelet.client"])
	}
	inst, err := server.getInstance(ctx, "knc123")
	if err != nil {
		t.Fatalf("getInstance: %v", err)
	}
	if inst.TenantID != "cluster-a" || inst.InstanceKind != "knc" {
		t.Fatalf("unexpected instance state: %#v", inst)
	}
	if inst.NonceJWT == "" || inst.NonceJWT != instanceEnv.Vars["NSTANCE_REGISTRATION_NONCE_JWT"] {
		t.Fatalf("unexpected instance nonce JWT")
	}
	for _, key := range []string{"NSTANCE_CA_CERT", "NSTANCE_REGISTRATION_NONCE_JWT", "NSTANCE_SERVER_REGISTRATION_ADDR", "NSTANCE_SERVER_AGENT_ADDR"} {
		if _, ok := instanceEnv.Vars[key]; !ok {
			t.Fatalf("missing instance env key %s", key)
		}
		if instanceEnv.Vars[key] == "" {
			t.Fatalf("empty instance env key %s", key)
		}
	}
}

func TestInstanceEnvWithAddrsDoesNotRequireStartedServer(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	server, err := New(Config{Store: store, ClusterID: "podplane-local", ShardID: "local"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := server.ConfigureInstance(ctx, InstanceRequest{TenantID: "cluster-a", InstanceID: "knc123", InstanceKind: "knc"}); err != nil {
		t.Fatalf("ConfigureInstance: %v", err)
	}
	instanceEnv, err := server.InstanceEnvWithAddrs(ctx, "knc123", ServerAddrs{RegistrationAddr: "10.0.2.2:1234", AgentAddr: "10.0.2.2:5678"})
	if err != nil {
		t.Fatalf("InstanceEnvWithAddrs: %v", err)
	}
	if instanceEnv.Vars["NSTANCE_SERVER_REGISTRATION_ADDR"] != "10.0.2.2:1234" {
		t.Fatalf("registration addr = %q", instanceEnv.Vars["NSTANCE_SERVER_REGISTRATION_ADDR"])
	}
	if instanceEnv.Vars["NSTANCE_SERVER_AGENT_ADDR"] != "10.0.2.2:5678" {
		t.Fatalf("agent addr = %q", instanceEnv.Vars["NSTANCE_SERVER_AGENT_ADDR"])
	}
	if instanceEnv.Vars["NSTANCE_REGISTRATION_NONCE_JWT"] == "" {
		t.Fatal("expected registration nonce JWT")
	}
	if instanceEnv.Vars["NSTANCE_CA_CERT"] == "" {
		t.Fatal("expected CA cert")
	}
}

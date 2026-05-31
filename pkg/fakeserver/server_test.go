// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
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
	claims := decodeNonceClaims(t, inst.NonceJWT)
	if claims.ConfigHash == "" {
		t.Fatal("expected nonce JWT to embed a non-empty config_hash claim")
	}
	if !strings.HasPrefix(claims.ConfigHash, "sha256:") {
		t.Fatalf("expected config_hash to use sha256 scheme, got %q", claims.ConfigHash)
	}
}

// TestConfigureInstanceWithoutTenantFails verifies fakeserver refuses to mint a
// registration nonce for an instance whose tenant has not been configured yet,
// matching the real server which always issues nonces in the context of a known
// group/tenant.
func TestConfigureInstanceWithoutTenantFails(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	server, err := New(Config{Store: store, ClusterID: "podplane-local", ShardID: "local"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = server.ConfigureInstance(ctx, InstanceRequest{TenantID: "cluster-a", InstanceID: "knc123", InstanceKind: "knc"})
	if err == nil {
		t.Fatal("expected ConfigureInstance to fail when tenant is missing")
	}
	if !strings.Contains(err.Error(), "ConfigureTenant") {
		t.Fatalf("expected error to mention ConfigureTenant, got: %v", err)
	}
}

func TestInstanceEnvWithAddrsDoesNotRequireStartedServer(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	server, err := New(Config{Store: store, ClusterID: "podplane-local", ShardID: "local"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := server.ConfigureTenant(ctx, TenantConfig{TenantID: "cluster-a"}); err != nil {
		t.Fatalf("ConfigureTenant: %v", err)
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

func TestReceiveFilesStaysOpenWhenNoFilesArePending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, api.ClientInfoKey, &api.ClientInfo{ClientID: "knc123", Role: "agent", Tenant: "cluster-a"})
	stream := &fakeReceiveFilesStream{ctx: ctx}
	service := &agentService{s: &Server{cfg: Config{Store: newMemoryStore()}}}

	done := make(chan error, 1)
	go func() {
		done <- service.ReceiveFiles(&emptypb.Empty{}, stream)
	}()

	select {
	case err := <-done:
		t.Fatalf("ReceiveFiles returned before cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReceiveFiles error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReceiveFiles did not return after cancellation")
	}
}

type fakeReceiveFilesStream struct {
	ctx   context.Context
	files []*proto.FileTransfer
}

func (s *fakeReceiveFilesStream) Send(file *proto.FileTransfer) error {
	s.files = append(s.files, file)
	return nil
}

func (s *fakeReceiveFilesStream) Context() context.Context { return s.ctx }

func (*fakeReceiveFilesStream) SetHeader(metadata.MD) error  { return nil }
func (*fakeReceiveFilesStream) SendHeader(metadata.MD) error { return nil }
func (*fakeReceiveFilesStream) SetTrailer(metadata.MD)       {}
func (*fakeReceiveFilesStream) SendMsg(any) error            { return nil }
func (*fakeReceiveFilesStream) RecvMsg(any) error            { return nil }

// decodeNonceClaims parses the unverified registration nonce JWT and returns
// its typed claims for assertions. The fake server's signing key is internal,
// so tests skip signature verification here.
func decodeNonceClaims(t *testing.T, nonceJWT string) *api.RegistrationNonceClaims {
	t.Helper()
	claims := &api.RegistrationNonceClaims{}
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(nonceJWT, claims); err != nil {
		t.Fatalf("parse nonce JWT: %v", err)
	}
	return claims
}

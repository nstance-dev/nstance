// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/nstance-dev/nstance/v2/internal/proto"
)

// tenantClient records service requests and supplies controllable RPC outcomes.
type tenantClient struct {
	proto.OperatorServiceClient
	sleepRequest  *proto.SleepTenantRequest
	wakeRequest   *proto.WakeTenantRequest
	statusRequest *proto.GetTenantStatusRequest
	sleepResult   proto.SleepTenantResponse_Result
	wakeResult    proto.WakeTenantResponse_Result
	sleepContext  context.Context
	err           error
}

// SleepTenant echoes the wake deadline and records whether activity is guarded.
func (c *tenantClient) SleepTenant(ctx context.Context, req *proto.SleepTenantRequest, _ ...grpc.CallOption) (*proto.SleepTenantResponse, error) {
	c.sleepContext = ctx
	c.sleepRequest = req
	return &proto.SleepTenantResponse{Result: c.sleepResult, Status: proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP, WakeAt: req.WakeAt}, c.err
}

// WakeTenant records explicit tenant wake requests without a listener filter.
func (c *tenantClient) WakeTenant(_ context.Context, req *proto.WakeTenantRequest, _ ...grpc.CallOption) (*proto.WakeTenantResponse, error) {
	c.wakeRequest = req
	return &proto.WakeTenantResponse{Result: c.wakeResult, Status: proto.TenantSleepStatus_TENANT_SLEEP_STATUS_AWAKE}, c.err
}

// GetTenantStatus returns missing observations as unknown rather than awake.
func (c *tenantClient) GetTenantStatus(_ context.Context, req *proto.GetTenantStatusRequest, _ ...grpc.CallOption) (*proto.GetTenantStatusResponse, error) {
	c.statusRequest = req
	return &proto.GetTenantStatusResponse{Listeners: []*proto.ListenerActivity{{Listener: "http"}}}, c.err
}

// tenantConnections records attempts and their contexts, including connection failures.
type tenantConnections struct {
	clients  map[string]*tenantClient
	shards   []string
	contexts []context.Context
	err      error
}

// Servers lists two configured shards in a deterministic order.
func (c *tenantConnections) Servers() []ShardServer {
	return []ShardServer{{ShardID: "a"}, {ShardID: "b"}}
}

// ConnectShard records the selected shard and its connection timeout context.
func (c *tenantConnections) ConnectShard(ctx context.Context, shard string) (*Connection, error) {
	c.shards = append(c.shards, shard)
	c.contexts = append(c.contexts, ctx)
	return &Connection{ShardID: shard, Client: c.clients[shard]}, c.err
}

// TestTenantSleep verifies guard mapping, idempotent success, and partial failure without rollback.
func TestTenantSleep(t *testing.T) {
	for _, force := range []bool{false, true} {
		busy := &tenantClient{sleepResult: proto.SleepTenantResponse_RESULT_BUSY}
		asleep := &tenantClient{sleepResult: proto.SleepTenantResponse_RESULT_ALREADY_ASLEEP}
		connector := &tenantConnections{clients: map[string]*tenantClient{"a": busy, "b": asleep}}
		wakeAt := time.Date(2026, 10, 9, 14, 0, 0, 0, time.FixedZone("offset", 2*60*60))
		started := time.Now()
		results, err := NewTenantService(connector).Sleep(context.Background(), TenantRequest{
			Tenant: "prod", AllShards: true, Timeout: 10 * time.Minute, Force: force, WakeAt: &wakeAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(connector.shards, []string{"a", "b"}) || len(results) != 2 {
			t.Fatalf("shards=%v results=%v", connector.shards, results)
		}
		if results[0].Shard != "a" || results[0].Error == nil || !strings.Contains(results[0].Error.Error(), "a: tenant prod sleep: sleep rejected: RESULT_BUSY") {
			t.Fatalf("busy outcome: %+v", results[0])
		}
		if results[1].Shard != "b" || results[1].Error != nil || results[1].Status != proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP || results[1].Result != "RESULT_ALREADY_ASLEEP" || results[1].WakeAt.AsTime().Format(time.RFC3339) != "2026-10-09T12:00:00Z" {
			t.Fatalf("successful outcome: %+v", results[1])
		}
		for _, client := range []*tenantClient{busy, asleep} {
			if client.sleepRequest == nil || client.sleepRequest.Tenant != "prod" || client.sleepRequest.IfNotBusy == force || !client.sleepRequest.WakeAt.AsTime().Equal(wakeAt) || client.wakeRequest != nil {
				t.Fatalf("incorrect sleep request or rollback: %+v", client)
			}
		}
		for i, ctx := range connector.contexts {
			if connector.clients[connector.shards[i]].sleepContext != ctx {
				t.Fatal("RPC did not use the shard's connection timeout context")
			}
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Before(started.Add(10*time.Minute)) || deadline.After(time.Now().Add(10*time.Minute)) || ctx.Err() != context.Canceled {
				t.Fatalf("per-shard context: deadline=%v error=%v", deadline, ctx.Err())
			}
		}
		if connector.contexts[0] == connector.contexts[1] {
			t.Fatal("shards shared one timeout context")
		}
	}
}

// TestTenantOutcomes verifies RPC errors, rejected transitions, and single-shard selection.
func TestTenantOutcomes(t *testing.T) {
	rpcErr := errors.New("permission denied")
	for _, tc := range []struct {
		name       string
		operation  string
		client     *tenantClient
		connectErr error
		wantError  string
	}{
		{name: "slept", operation: "sleep", client: &tenantClient{sleepResult: proto.SleepTenantResponse_RESULT_SLEPT}},
		{name: "rejected sleep", operation: "sleep", client: &tenantClient{}, wantError: "sleep rejected: RESULT_UNSPECIFIED"},
		{name: "woke", operation: "wake", client: &tenantClient{wakeResult: proto.WakeTenantResponse_RESULT_WOKE}},
		{name: "already awake", operation: "wake", client: &tenantClient{wakeResult: proto.WakeTenantResponse_RESULT_ALREADY_AWAKE}},
		{name: "rejected wake", operation: "wake", client: &tenantClient{}, wantError: "wake rejected: RESULT_UNSPECIFIED"},
		{name: "status", operation: "status", client: &tenantClient{}},
		{name: "sleep RPC", operation: "sleep", client: &tenantClient{err: rpcErr}, wantError: rpcErr.Error()},
		{name: "wake RPC", operation: "wake", client: &tenantClient{err: rpcErr}, wantError: rpcErr.Error()},
		{name: "status RPC", operation: "status", client: &tenantClient{err: rpcErr}, wantError: rpcErr.Error()},
		{name: "connection", operation: "sleep", client: &tenantClient{}, connectErr: rpcErr, wantError: rpcErr.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &tenantConnections{clients: map[string]*tenantClient{"b": tc.client}, err: tc.connectErr}
			svc := NewTenantService(connector)
			req := TenantRequest{Tenant: "prod", Shard: "b", Timeout: time.Minute}
			operations := map[string]func(context.Context, TenantRequest) ([]TenantResult, error){"sleep": svc.Sleep, "wake": svc.Wake, "status": svc.Status}
			results, err := operations[tc.operation](context.Background(), req)
			if err != nil || len(results) != 1 || !reflect.DeepEqual(connector.shards, []string{"b"}) {
				t.Fatalf("shards=%v results=%v error=%v", connector.shards, results, err)
			}
			result := results[0]
			if tc.wantError != "" {
				if result.Error == nil || !strings.Contains(result.Error.Error(), tc.wantError) {
					t.Fatalf("want %q, got %+v", tc.wantError, result)
				}
				if (tc.client.err != nil || tc.connectErr != nil) && !errors.Is(result.Error, rpcErr) {
					t.Fatal("RPC/connection error lost its cause")
				}
				return
			}
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			switch tc.operation {
			case "sleep":
				if tc.client.sleepRequest.Tenant != "prod" || !tc.client.sleepRequest.IfNotBusy || tc.client.sleepRequest.WakeAt != nil || result.Status != proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP {
					t.Fatalf("sleep: %+v %+v", tc.client.sleepRequest, result)
				}
			case "wake":
				if tc.client.wakeRequest.Tenant != "prod" || tc.client.wakeRequest.Listener != nil || result.Status != proto.TenantSleepStatus_TENANT_SLEEP_STATUS_AWAKE {
					t.Fatalf("wake: %+v %+v", tc.client.wakeRequest, result)
				}
			case "status":
				if tc.client.statusRequest.Tenant != "prod" || result.Status != proto.TenantSleepStatus_TENANT_SLEEP_STATUS_UNSPECIFIED || result.WakeAt != nil || len(result.Listeners) != 1 || result.Listeners[0].GetAvailable() || result.Listeners[0].GetIdleSince() != nil {
					t.Fatalf("status: %+v %+v", tc.client.statusRequest, result)
				}
			}
		})
	}
}

// TestTenantValidation ensures service callers cannot bypass validation before connections.
func TestTenantValidation(t *testing.T) {
	invalidWakeAt := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, req := range []TenantRequest{
		{Tenant: "bad/name", Shard: "a", Timeout: time.Minute},
		{Tenant: "prod", Timeout: time.Minute},
		{Tenant: "prod", Shard: "a", AllShards: true, Timeout: time.Minute},
		{Tenant: "prod", Shard: "missing", Timeout: time.Minute},
		{Tenant: "prod", Shard: "a"},
		{Tenant: "prod", Shard: "a", Timeout: -time.Second},
		{Tenant: "prod", Shard: "a", Timeout: time.Minute, WakeAt: &invalidWakeAt},
	} {
		connector := &tenantConnections{}
		results, err := NewTenantService(connector).Sleep(context.Background(), req)
		if err == nil || len(results) != 0 || len(connector.shards) != 0 {
			t.Fatalf("invalid request reached a shard: request=%+v results=%v error=%v", req, results, err)
		}
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/v2/internal/admin/service"
	"github.com/nstance-dev/nstance/v2/internal/proto"
)

// tenantTestClient records the actual requests delivered to the RPC client.
type tenantTestClient struct {
	proto.OperatorServiceClient
	sleep          *proto.SleepTenantRequest
	wake           *proto.WakeTenantRequest
	status         *proto.GetTenantStatusRequest
	result         proto.SleepTenantResponse_Result
	err            error
	statusResponse *proto.GetTenantStatusResponse
	sleepWait      <-chan struct{}
}

// SleepTenant records the guarded/forced mapping and echoes the wake deadline.
func (c *tenantTestClient) SleepTenant(ctx context.Context, req *proto.SleepTenantRequest, _ ...grpc.CallOption) (*proto.SleepTenantResponse, error) {
	c.sleep = req
	if c.sleepWait != nil {
		select {
		case <-c.sleepWait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &proto.SleepTenantResponse{Result: c.result, Status: proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP, WakeAt: req.WakeAt}, c.err
}

// WakeTenant records tenant selection and returns a successful wake.
func (c *tenantTestClient) WakeTenant(_ context.Context, req *proto.WakeTenantRequest, _ ...grpc.CallOption) (*proto.WakeTenantResponse, error) {
	c.wake = req
	return &proto.WakeTenantResponse{Result: proto.WakeTenantResponse_RESULT_WOKE, Status: proto.TenantSleepStatus_TENANT_SLEEP_STATUS_AWAKE}, c.err
}

// GetTenantStatus records the tenant and supplies both available and missing observations.
func (c *tenantTestClient) GetTenantStatus(_ context.Context, req *proto.GetTenantStatusRequest, _ ...grpc.CallOption) (*proto.GetTenantStatusResponse, error) {
	c.status = req
	if c.statusResponse != nil {
		return c.statusResponse, c.err
	}
	stamp := timestamppb.New(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	return &proto.GetTenantStatusResponse{Status: proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP, WakeAt: stamp, Listeners: []*proto.ListenerActivity{
		{Listener: "http", Available: true, IdleSince: stamp}, {Listener: "tcp", Available: false},
	}}, c.err
}

// tenantTestConnector records shard selection without reading identity or dialing.
type tenantTestConnector struct {
	client     *tenantTestClient
	clients    map[string]*tenantTestClient
	shards     []string
	closed     bool
	connectErr error
}

// ConnectShard records the selected shard and returns the recording RPC client.
func (c *tenantTestConnector) ConnectShard(_ context.Context, shard string) (*service.Connection, error) {
	c.shards = append(c.shards, shard)
	client := c.client
	if c.clients != nil {
		client = c.clients[shard]
	}
	return &service.Connection{ShardID: shard, Client: client}, c.connectErr
}

// Servers provides two independent configured shards for selection tests.
func (c *tenantTestConnector) Servers() []service.ShardServer {
	return []service.ShardServer{{ShardID: "a"}, {ShardID: "b"}}
}

// Close records connector cleanup, including on failed RPCs.
func (c *tenantTestConnector) Close() { c.closed = true }

// TestTenantRequests verifies CLI flags reach the selected shard and existing RPCs.
func TestTenantRequests(t *testing.T) {
	for _, operation := range []string{"sleep", "forced", "wake", "status"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv(envAdminServers, "a=localhost:1,b=localhost:2")
			t.Setenv(envAdminShard, "a")
			t.Setenv(envAdminIdentityDir, "/environment-identity")
			client := &tenantTestClient{result: proto.SleepTenantResponse_RESULT_SLEPT}
			connector := &tenantTestConnector{client: client}
			cmd := newTenantCommand(func(servers, identityDir string, timeout time.Duration) (tenantConnector, error) {
				if servers != "a=localhost:3,b=localhost:4" || identityDir != "/explicit-identity" || timeout != 10*time.Minute {
					t.Fatalf("connector arguments: %s %s %s", servers, identityDir, timeout)
				}
				return connector, nil
			})
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			verb := operation
			if verb == "forced" {
				verb = "sleep"
			}
			args := []string{verb, "prod", "--shard", "b", "--servers", "a=localhost:3,b=localhost:4", "--identity-dir", "/explicit-identity"}
			if operation == "forced" {
				args = append(args, "--force", "--wake-at", "2026-10-09T14:00:00+02:00")
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(connector.shards, []string{"b"}) || !connector.closed {
				t.Fatalf("connector: %+v", connector)
			}
			if !strings.Contains(out.String(), "b: tenant prod") {
				t.Fatal(out.String())
			}
			switch operation {
			case "sleep", "forced":
				if client.sleep.Tenant != "prod" || client.sleep.IfNotBusy != (operation == "sleep") {
					t.Fatalf("request: %v", client.sleep)
				}
				if operation == "forced" {
					if client.sleep.WakeAt == nil || client.sleep.WakeAt.AsTime().Format(time.RFC3339) != "2026-10-09T12:00:00Z" {
						t.Fatalf("deadline: %v", client.sleep)
					}
					if !strings.Contains(out.String(), "asleep") || !strings.Contains(out.String(), "wake-at=2026-10-09T12:00:00Z") || !strings.Contains(stderr.String(), "interrupt workloads and active connections") {
						t.Fatalf("output: %s %s", &out, &stderr)
					}
				} else if client.sleep.WakeAt != nil || strings.Contains(stderr.String(), "Warning:") {
					t.Fatal("guarded sleep gained deadline or warning")
				}
			case "wake":
				if client.wake.Tenant != "prod" || client.wake.Listener != nil || !strings.Contains(out.String(), "awake (RESULT_WOKE)") {
					t.Fatalf("wake: %v %s", client.wake, &out)
				}
			case "status":
				if client.status.Tenant != "prod" {
					t.Fatal(client.status)
				}
				for _, expected := range []string{"asleep wake-at=2026-10-09T12:00:00Z", "http: activity=available, idle-since=2026-10-09T12:00:00Z", "tcp: activity=unavailable, idle-since=unknown"} {
					if !strings.Contains(out.String(), expected) {
						t.Fatalf("missing %q in %s", expected, &out)
					}
				}
			}
		})
	}
}

// TestTenantSleepProgress verifies feedback appears while the server has not replied.
func TestTenantSleepProgress(t *testing.T) {
	t.Setenv(envAdminShard, "a")
	t.Setenv(envAdminServers, "a=localhost:1")
	synctest.Test(t, func(t *testing.T) {
		reply := make(chan struct{})
		client := &tenantTestClient{result: proto.SleepTenantResponse_RESULT_SLEPT, sleepWait: reply}
		connector := &tenantTestConnector{client: client}
		cmd := newTenantCommand(func(string, string, time.Duration) (tenantConnector, error) { return connector, nil })
		var out, stderr bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"sleep", "prod", "--force"})
		done := make(chan error, 1)
		go func() { done <- cmd.Execute() }()
		synctest.Wait()
		time.Sleep(16 * time.Second)
		synctest.Wait()
		if !strings.Contains(stderr.String(), "Requesting sleep for tenant prod") || !strings.Contains(stderr.String(), "does not wait for existing connections to drain") {
			t.Errorf("missing immediate feedback: %s", &stderr)
		}
		if !strings.Contains(stderr.String(), "Still waiting for tenant prod sleep (15s elapsed); no completion response yet.") {
			t.Errorf("missing waiting feedback: %s", &stderr)
		}
		if out.Len() != 0 {
			t.Errorf("reported success before server replied: %s", &out)
		}
		close(reply)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "asleep (RESULT_SLEPT)") {
			t.Fatalf("missing completion: %s", &out)
		}
	})
}

// TestTenantFailures verifies failures propagate to Cobra (and thus the binary exit code).
func TestTenantFailures(t *testing.T) {
	for _, failure := range []string{"BUSY", "connection"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv(envAdminShard, "")
			client := &tenantTestClient{result: proto.SleepTenantResponse_RESULT_BUSY}
			connector := &tenantTestConnector{client: client}
			if failure == "connection" {
				connector.connectErr = errors.New("connection unavailable")
			}
			cmd := newTenantCommand(func(string, string, time.Duration) (tenantConnector, error) { return connector, nil })
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs([]string{"sleep", "prod", "--servers", "a=localhost:1,b=localhost:2", "--all-shards"})
			if err := cmd.Execute(); err == nil {
				t.Fatal("failure returned success")
			}
			if !reflect.DeepEqual(connector.shards, []string{"a", "b"}) || !connector.closed {
				t.Fatalf("connector: %+v", connector)
			}
			for _, expected := range []string{"a: tenant prod sleep:", "b: tenant prod sleep:"} {
				if !strings.Contains(output.String(), expected) {
					t.Fatal(output.String())
				}
			}
			if failure == "BUSY" && !strings.Contains(output.String(), "RESULT_BUSY") {
				t.Fatal(output.String())
			}
		})
	}
}

// TestTenantPartialSleep verifies a rejection neither hides a successful shard nor triggers rollback.
func TestTenantPartialSleep(t *testing.T) {
	t.Setenv(envAdminShard, "")
	busy := &tenantTestClient{result: proto.SleepTenantResponse_RESULT_BUSY}
	slept := &tenantTestClient{result: proto.SleepTenantResponse_RESULT_SLEPT}
	connector := &tenantTestConnector{clients: map[string]*tenantTestClient{"a": busy, "b": slept}}
	cmd := newTenantCommand(func(string, string, time.Duration) (tenantConnector, error) { return connector, nil })
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"sleep", "prod", "--servers", "a=localhost:1,b=localhost:2", "--all-shards"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("partial failure returned success")
	}
	if !reflect.DeepEqual(connector.shards, []string{"a", "b"}) || !connector.closed {
		t.Fatalf("connector: %+v", connector)
	}
	if busy.sleep == nil || slept.sleep == nil || busy.wake != nil || slept.wake != nil {
		t.Fatal("sleep skipped a shard or attempted rollback")
	}
	if !strings.Contains(stderr.String(), "a: tenant prod sleep: sleep rejected: RESULT_BUSY") ||
		!strings.Contains(out.String(), "b: tenant prod asleep (RESULT_SLEPT)") {
		t.Fatalf("partial results: stdout=%s stderr=%s", &out, &stderr)
	}
}

// TestTenantValidation ensures invalid inputs fail before identity loading or RPCs.
func TestTenantValidation(t *testing.T) {
	t.Setenv(envAdminServers, "")
	t.Setenv(envAdminShard, "")
	for _, args := range [][]string{
		{"sleep"}, {"sleep", "prod", "extra"}, {"sleep", ""}, {"sleep", "bad/name"},
		{"sleep", "prod"}, {"sleep", "prod", "--shard", "a"},
		{"sleep", "prod", "--servers", "malformed", "--shard", "a"},
		{"sleep", "prod", "--servers", "a=localhost:1", "--shard", "b"},
		{"sleep", "prod", "--servers", "a=localhost:1", "--shard", "a", "--all-shards"},
		{"sleep", "prod", "--servers", "a=localhost:1", "--shard", "a", "--timeout", "0s"},
		{"sleep", "prod", "--servers", "a=localhost:1", "--shard", "a", "--timeout", "-1s"},
		{"sleep", "prod", "--servers", "a=localhost:1", "--shard", "a", "--timeout", "bad"},
		{"sleep", "prod", "--servers", "a=localhost:1", "--shard", "a", "--wake-at", "tomorrow"},
		{"sleep", "prod", "--servers", "a=localhost:1", "--shard", "a", "--wake-at", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := newTenantCommand(func(string, string, time.Duration) (tenantConnector, error) {
				t.Fatal("invalid input loaded identity")
				return nil, nil
			})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err == nil {
				t.Fatal("invalid input succeeded")
			}
		})
	}
}

// TestTenantEnvironment verifies environment defaults and errors from read/wake RPCs.
func TestTenantEnvironment(t *testing.T) {
	t.Setenv(envAdminServers, "a=localhost:1,b=localhost:2")
	t.Setenv(envAdminShard, "a")
	t.Setenv(envAdminIdentityDir, "/environment-identity")
	for _, operation := range []string{"status", "wake"} {
		for _, fail := range []bool{false, true} {
			client := &tenantTestClient{}
			if fail {
				client.err = errors.New("permission denied")
			}
			connector := &tenantTestConnector{client: client}
			cmd := newTenantCommand(func(servers, identityDir string, timeout time.Duration) (tenantConnector, error) {
				if servers != "a=localhost:1,b=localhost:2" || identityDir != "/environment-identity" || timeout != time.Minute {
					t.Fatalf("environment/timeout mapping: %s %s %s", servers, identityDir, timeout)
				}
				return connector, nil
			})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs([]string{operation, "prod", "--timeout", "1m"})
			if err := cmd.Execute(); (err != nil) != fail {
				t.Fatalf("%s fail=%v: %v", operation, fail, err)
			}
			if !reflect.DeepEqual(connector.shards, []string{"a"}) {
				t.Fatal(connector.shards)
			}
			if fail && !strings.Contains(output.String(), "permission denied") {
				t.Fatal(output.String())
			}
		}
	}
}

// TestTenantUnknownStatus preserves unknown states and omits absent wake deadlines.
func TestTenantUnknownStatus(t *testing.T) {
	t.Setenv(envAdminShard, "")
	for _, status := range []proto.TenantSleepStatus{proto.TenantSleepStatus_TENANT_SLEEP_STATUS_UNSPECIFIED, 99} {
		client := &tenantTestClient{statusResponse: &proto.GetTenantStatusResponse{Status: status}}
		connector := &tenantTestConnector{client: client}
		cmd := newTenantCommand(func(string, string, time.Duration) (tenantConnector, error) { return connector, nil })
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs([]string{"status", "prod", "--servers", "a=localhost:1", "--shard", "a"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if output.String() != "a: tenant prod unknown\n" {
			t.Fatalf("unexpected status output: %q", output.String())
		}
	}
}

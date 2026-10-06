// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/nstance-dev/nstance/v2/internal/proto"
)

// fakeRunner records starts and exits when its context is canceled.
type fakeRunner struct {
	mu      sync.Mutex
	runs    int
	started chan context.Context
}

// Run reports readiness and waits for cancellation.
func (r *fakeRunner) Run(ctx context.Context, _ string, ready func()) error {
	r.mu.Lock()
	r.runs++
	r.mu.Unlock()
	r.started <- ctx
	ready()
	<-ctx.Done()
	return ctx.Err()
}

// streamServer exposes supervisor connections to a test.
type streamServer struct {
	proto.UnimplementedTunnelServiceServer
	connected chan grpc.BidiStreamingServer[proto.TunnelStatus, proto.TunnelDesiredState]
}

// Manage records an initiated supervisor connection.
func (s *streamServer) Manage(stream grpc.BidiStreamingServer[proto.TunnelStatus, proto.TunnelDesiredState]) error {
	s.connected <- stream
	<-stream.Context().Done()
	return stream.Context().Err()
}

// TestServiceInitiatesConnection verifies the supervisor dials the server and reports status.
func TestServiceInitiatesConnection(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "nstance-tunnel-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "tunnel.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	fixture := &streamServer{connected: make(chan grpc.BidiStreamingServer[proto.TunnelStatus, proto.TunnelDesiredState], 1)}
	proto.RegisterTunnelServiceServer(server, fixture)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	runner := &fakeRunner{started: make(chan context.Context, 1)}
	service := newTestService(t, path, runner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	stream := <-fixture.connected
	if err := stream.Send(runningState(1, 5)); err != nil {
		t.Fatal(err)
	}
	if status := receiveStatus(t, stream); status.State != proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING {
		t.Fatalf("first state = %s", status.State)
	}
	if status := receiveStatus(t, stream); status.State != proto.TunnelStatus_TUNNEL_STATUS_STATE_READY {
		t.Fatalf("second state = %s", status.State)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
}

// TestLeaseSurvivesDisconnectAndExpires verifies bounded fail-closed operation.
func TestLeaseSurvivesDisconnectAndExpires(t *testing.T) {
	runner := &fakeRunner{started: make(chan context.Context, 1)}
	service := newTestService(t, "/unused", runner)
	if err := service.apply(runningState(1, 1)); err != nil {
		t.Fatal(err)
	}
	run := <-runner.started
	time.Sleep(600 * time.Millisecond)
	if err := service.apply(runningState(1, 1)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	select {
	case <-run.Done():
		t.Fatal("tunnel stopped before its renewed lease expired")
	default:
	}
	select {
	case <-run.Done():
	case <-time.After(700 * time.Millisecond):
		t.Fatal("tunnel remained running after lease expiry")
	}
}

// TestExplicitStopRevokesImmediately verifies a stop does not wait for lease expiry.
func TestExplicitStopRevokesImmediately(t *testing.T) {
	runner := &fakeRunner{started: make(chan context.Context, 1)}
	service := newTestService(t, "/unused", runner)
	if err := service.apply(runningState(1, 30)); err != nil {
		t.Fatal(err)
	}
	run := <-runner.started
	if err := service.apply(&proto.TunnelDesiredState{TunnelName: "known", Revision: 2, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-run.Done():
	case <-time.After(time.Second):
		t.Fatal("explicit stop did not cancel the tunnel")
	}
	if status := awaitUpdate(t, service.updates, proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED); status.Revision != 2 {
		t.Fatalf("stopped revision = %d", status.Revision)
	}
}

// TestNewLeaderRevisionKeepsRunning verifies leadership handoff does not restart a healthy Pod.
func TestNewLeaderRevisionKeepsRunning(t *testing.T) {
	runner := &fakeRunner{started: make(chan context.Context, 2)}
	service := newTestService(t, "/unused", runner)
	if err := service.apply(runningState(1, 30)); err != nil {
		t.Fatal(err)
	}
	run := <-runner.started
	if err := service.apply(runningState(2, 30)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
		t.Fatal("new leader revision restarted the tunnel")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case <-run.Done():
		t.Fatal("new leader revision stopped the tunnel")
	default:
	}
	service.stopAll()
}

// TestServiceRejectsUnknownTunnel verifies the local allowlist.
func TestServiceRejectsUnknownTunnel(t *testing.T) {
	service := newTestService(t, "/unused", &fakeRunner{started: make(chan context.Context, 1)})
	err := service.apply(&proto.TunnelDesiredState{TunnelName: "unknown", Revision: 1, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING, LeaseSeconds: 1})
	if err == nil {
		t.Fatal("unknown tunnel was accepted")
	}
}

// newTestService creates a service with one allowlisted tunnel.
func newTestService(t *testing.T, socket string, runner Runner) *Service {
	t.Helper()
	service, err := NewService(socket, []string{"known"}, runner, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// runningState builds one leased running request.
func runningState(revision uint64, leaseSeconds uint32) *proto.TunnelDesiredState {
	return &proto.TunnelDesiredState{TunnelName: "known", Revision: revision, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING, LeaseSeconds: leaseSeconds}
}

// receiveStatus receives one status from a server-side stream.
func receiveStatus(t *testing.T, stream grpc.BidiStreamingServer[proto.TunnelStatus, proto.TunnelDesiredState]) *proto.TunnelStatus {
	t.Helper()
	status, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	return status
}

// awaitUpdate skips queued statuses until the requested state arrives.
func awaitUpdate(t *testing.T, updates <-chan *proto.TunnelStatus, state proto.TunnelStatus_State) *proto.TunnelStatus {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case status := <-updates:
			if status.State == state {
				return status
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", state)
			return nil
		}
	}
}

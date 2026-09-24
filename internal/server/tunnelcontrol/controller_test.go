// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnelcontrol

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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/internal/proto"
)

// testService records tunnel control sessions and returns fixture statuses.
type testService struct {
	proto.UnimplementedTunnelServiceServer

	mu       sync.Mutex
	sessions int
	received chan received
	closed   chan int
}

// received pairs a desired-state request with its test session.
type received struct {
	session int
	request *proto.TunnelDesiredState
}

// Manage records requests and sends tunnel-selected fixture statuses.
func (s *testService) Manage(stream grpc.BidiStreamingServer[proto.TunnelDesiredState, proto.TunnelStatus]) error {
	s.mu.Lock()
	s.sessions++
	session := s.sessions
	s.mu.Unlock()
	defer func() { s.closed <- session }()
	for {
		request, err := stream.Recv()
		if err != nil {
			return err
		}
		s.received <- received{session: session, request: request}
		switch request.TunnelName {
		case "ready":
			if err := stream.Send(&proto.TunnelStatus{TunnelName: request.TunnelName, Revision: request.Revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_READY}); err != nil {
				return err
			}
		case "failed":
			text := "runner failed"
			if err := stream.Send(&proto.TunnelStatus{TunnelName: request.TunnelName, Revision: request.Revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED, Error: &text}); err != nil {
				return err
			}
		case "stopped":
			if err := stream.Send(&proto.TunnelStatus{TunnelName: request.TunnelName, Revision: request.Revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED}); err != nil {
				return err
			}
		case "drop":
			return status.Error(codes.Unavailable, "test disconnect")
		}
	}
}

// TestControllerStatusesAndStop verifies terminal statuses and clean shutdown.
func TestControllerStatusesAndStop(t *testing.T) {
	controller, service, cleanup := startTestController(t)
	defer cleanup()

	tests := []struct {
		tunnelName string
		desired    proto.TunnelDesiredState_State
		want       proto.TunnelStatus_State
	}{
		{"ready", proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING, proto.TunnelStatus_TUNNEL_STATUS_STATE_READY},
		{"failed", proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING, proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED},
		{"stopped", proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED, proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED},
	}
	for _, test := range tests {
		revision, err := controller.SetDesired(test.tunnelName, test.desired)
		if err != nil {
			t.Fatal(err)
		}
		got, err := controller.Wait(testContext(t), test.tunnelName, revision)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != test.want || got.Revision != revision {
			t.Fatalf("%s status = %v revision %d, want %v revision %d", test.tunnelName, got.State, got.Revision, test.want, revision)
		}
	}

	controller.Stop()
	select {
	case <-service.closed:
	case <-time.After(time.Second):
		t.Fatal("Stop did not close Manage stream")
	}
}

// TestControllerReconnectsAndReasserts verifies desired state survives reconnects.
func TestControllerReconnectsAndReasserts(t *testing.T) {
	controller, service, cleanup := startTestController(t)
	defer cleanup()

	revision, err := controller.SetDesired("drop", proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING)
	if err != nil {
		t.Fatal(err)
	}
	first := awaitRequest(t, service.received)
	if first.session != 1 || first.request.Revision == 0 {
		t.Fatalf("first request = session %d revision %d", first.session, first.request.Revision)
	}
	second := awaitRequest(t, service.received)
	if second.session < 2 || second.request.TunnelName != "drop" || second.request.Revision == 0 {
		t.Fatalf("reasserted request = %#v", second)
	}
	if _, err := controller.Wait(testContext(t), "drop", revision); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Wait error = %v, want ErrUnavailable", err)
	}
	retryRevision, err := controller.SetDesired("drop", proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING)
	if err != nil {
		t.Fatal(err)
	}
	if retryRevision == revision {
		t.Fatal("retry after unavailable result reused failed logical revision")
	}
}

// TestControllerWaitCleansUpAfterCancellationAndStop verifies waits cannot leak or strand.
func TestControllerWaitCleansUpAfterCancellationAndStop(t *testing.T) {
	controller, err := New(filepath.Join(t.TempDir(), "tunnel.sock"), slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := controller.Wait(ctx, "api", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Wait error = %v, want context.Canceled", err)
	}
	if len(controller.waiters) != 0 {
		t.Fatalf("canceled Wait retained %d waiter sets", len(controller.waiters))
	}
	controller.Stop()
	if _, err := controller.Wait(context.Background(), "api", 2); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Wait after Stop error = %v, want ErrUnavailable", err)
	}
}

// startTestController starts a controller and local fixture service.
func startTestController(t *testing.T) (*Controller, *testService, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "nstance-tunnel-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "tunnel.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	service := &testService{received: make(chan received, 32), closed: make(chan int, 32)}
	server := grpc.NewServer()
	proto.RegisterTunnelServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	controller, err := New(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return controller, service, func() {
		controller.Stop()
		server.Stop()
	}
}

// awaitRequest waits for one fixture request or fails the test.
func awaitRequest(t *testing.T, requests <-chan received) received {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Manage request")
		return received{}
	}
}

// testContext returns a bounded context tied to the test cleanup.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

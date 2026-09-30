// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnelcontrol

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/nstance-dev/nstance/internal/proto"
)

// TestControllerStreamsLeasesAndRevokesOnStop verifies the complete leader-owned stream lifecycle.
func TestControllerStreamsLeasesAndRevokesOnStop(t *testing.T) {
	controller, stream := startController(t)
	revision, err := controller.SetDesired("api", proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING)
	if err != nil {
		t.Fatal(err)
	}
	running := receiveDesired(t, stream)
	if running.TunnelName != "api" || running.Revision != revision || running.LeaseSeconds == 0 {
		t.Fatalf("running state = %#v", running)
	}
	if err := stream.Send(&proto.TunnelStatus{TunnelName: "api", Revision: revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_READY}); err != nil {
		t.Fatal(err)
	}
	status, err := controller.Wait(testContext(t), "api", revision)
	if err != nil || status.GetState() != proto.TunnelStatus_TUNNEL_STATUS_STATE_READY {
		t.Fatalf("Wait() = %v, %v", status, err)
	}
	heartbeat := receiveDesired(t, stream)
	if heartbeat.Revision != revision || heartbeat.State != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING {
		t.Fatalf("heartbeat = %#v", heartbeat)
	}

	stopped := make(chan struct{})
	go func() {
		controller.Stop()
		close(stopped)
	}()
	revocation := receiveDesired(t, stream)
	if revocation.State != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED || revocation.Revision == revision || revocation.LeaseSeconds != 0 {
		t.Fatalf("revocation = %#v", revocation)
	}
	if err := stream.Send(&proto.TunnelStatus{TunnelName: "api", Revision: revocation.Revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not finish after revocation acknowledgement")
	}
}

// TestControllerRejectsConcurrentSupervisor verifies only one privileged supervisor may connect.
func TestControllerRejectsConcurrentSupervisor(t *testing.T) {
	controller, _ := startController(t)
	connection, err := grpc.NewClient("unix://"+controller.socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	stream, err := proto.NewTunnelServiceClient(connection).Manage(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("second supervisor connection succeeded")
	}
}

// TestControllerIgnoresStaleStatus verifies an old revision cannot complete a current wait.
func TestControllerIgnoresStaleStatus(t *testing.T) {
	controller, stream := startController(t)
	revision, err := controller.SetDesired("api", proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING)
	if err != nil {
		t.Fatal(err)
	}
	_ = receiveDesired(t, stream)
	if err := stream.Send(&proto.TunnelStatus{TunnelName: "api", Revision: revision + 1, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_READY}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := controller.Wait(ctx, "api", revision); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait error = %v, want deadline exceeded", err)
	}
}

// TestControllerRetriesFailedRevision verifies a failed run gets new authority.
func TestControllerRetriesFailedRevision(t *testing.T) {
	controller, stream := startController(t)
	revision, err := controller.SetDesired("api", proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING)
	if err != nil {
		t.Fatal(err)
	}
	_ = receiveDesired(t, stream)
	if err := stream.Send(&proto.TunnelStatus{TunnelName: "api", Revision: revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED}); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Wait(testContext(t), "api", revision); err != nil {
		t.Fatal(err)
	}
	retry, err := controller.SetDesired("api", proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING)
	if err != nil {
		t.Fatal(err)
	}
	if retry == revision {
		t.Fatal("failed revision was reused")
	}
}

// TestListenRefusesNonSocket verifies startup cannot replace an arbitrary file.
func TestListenRefusesNonSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.sock")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	controller, err := New(path, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(); err == nil {
		t.Fatal("Start replaced a non-socket path")
	}
}

// startController starts a controller and returns its first supervisor stream.
func startController(t *testing.T) (*Controller, proto.TunnelService_ManageClient) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "nstance-tunnel-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "tunnel.sock")
	controller, err := New(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	controller.heartbeat = 20 * time.Millisecond
	controller.lease = time.Second
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.Stop)
	connection, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	stream, err := proto.NewTunnelServiceClient(connection).Manage(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		controller.mu.Lock()
		connected := controller.session != nil
		controller.mu.Unlock()
		if connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervisor did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	return controller, stream
}

// receiveDesired waits for one server state update.
func receiveDesired(t *testing.T, stream proto.TunnelService_ManageClient) *proto.TunnelDesiredState {
	t.Helper()
	result := make(chan struct {
		state *proto.TunnelDesiredState
		err   error
	}, 1)
	go func() {
		state, err := stream.Recv()
		result <- struct {
			state *proto.TunnelDesiredState
			err   error
		}{state: state, err: err}
	}()
	select {
	case item := <-result:
		if item.err != nil {
			t.Fatal(item.err)
		}
		return item.state
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tunnel desired state")
		return nil
	}
}

// testContext returns a bounded test context.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

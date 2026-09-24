// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/internal/proto"
)

// fakeRunner records runs and optionally reports readiness or failure.
type fakeRunner struct {
	mu      sync.Mutex
	runs    int
	started chan context.Context
	ready   bool
	fail    error
}

// delayedExitRunner blocks shutdown to expose overlapping runs.
type delayedExitRunner struct {
	started chan struct{}
	exit    chan struct{}
	mu      sync.Mutex
	active  int
	max     int
}

// Run blocks process exit until the fixture permits it.
func (r *delayedExitRunner) Run(ctx context.Context, _ string, _ func()) error {
	r.mu.Lock()
	r.active++
	if r.active > r.max {
		r.max = r.active
	}
	r.mu.Unlock()
	r.started <- struct{}{}
	<-ctx.Done()
	<-r.exit
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	return ctx.Err()
}

// Run records a fixture run and follows its configured behavior.
func (r *fakeRunner) Run(ctx context.Context, _ string, ready func()) error {
	r.mu.Lock()
	r.runs++
	shouldReady, fail := r.ready, r.fail
	r.mu.Unlock()
	select {
	case r.started <- ctx:
	default:
	}
	if shouldReady {
		ready()
	}
	if fail != nil {
		return fail
	}
	<-ctx.Done()
	return ctx.Err()
}

// fakeStream implements the tunnel management stream for tests.
type fakeStream struct {
	ctx  context.Context
	recv chan *proto.TunnelDesiredState
	sent chan *proto.TunnelStatus
}

// newFakeStream creates a buffered fixture stream.
func newFakeStream(ctx context.Context) *fakeStream {
	return &fakeStream{ctx: ctx, recv: make(chan *proto.TunnelDesiredState, 8), sent: make(chan *proto.TunnelStatus, 32)}
}

// Context returns the fixture stream context.
func (s *fakeStream) Context() context.Context { return s.ctx }

// Send records a server status message.
func (s *fakeStream) Send(message *proto.TunnelStatus) error { s.sent <- message; return nil }

// Recv returns the next client desired-state message.
func (s *fakeStream) Recv() (*proto.TunnelDesiredState, error) {
	message, ok := <-s.recv
	if !ok {
		return nil, io.EOF
	}
	return message, nil
}

// SetHeader implements grpc.ServerStream.
func (s *fakeStream) SetHeader(metadata.MD) error { return nil }

// SendHeader implements grpc.ServerStream.
func (s *fakeStream) SendHeader(metadata.MD) error { return nil }

// SetTrailer implements grpc.ServerStream.
func (s *fakeStream) SetTrailer(metadata.MD) {}

// SendMsg implements grpc.ServerStream.
func (s *fakeStream) SendMsg(any) error { return nil }

// RecvMsg implements grpc.ServerStream.
func (s *fakeStream) RecvMsg(any) error { return nil }

// newTestService creates a service with one allowed tunnel name.
func newTestService(t *testing.T, runner Runner, minBackoff time.Duration) *Service {
	t.Helper()
	service, err := NewService([]string{"known"}, runner, minBackoff, 4*minBackoff)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// receiveState waits for and verifies one status.
func receiveState(t *testing.T, stream *fakeStream, wanted proto.TunnelStatus_State) *proto.TunnelStatus {
	t.Helper()
	select {
	case message := <-stream.sent:
		if message.State != wanted {
			t.Fatalf("state = %s, want %s", message.State, wanted)
		}
		return message
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", wanted)
	}
	return nil
}

// TestUnknownTunnel verifies the allowlist rejects unknown tunnel names.
func TestUnknownTunnel(t *testing.T) {
	service := newTestService(t, &fakeRunner{started: make(chan context.Context, 1)}, time.Millisecond)
	stream := newFakeStream(context.Background())
	stream.recv <- &proto.TunnelDesiredState{TunnelName: "unknown", Revision: 1, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING}
	if code := status.Code(service.Manage(stream)); code != codes.PermissionDenied {
		t.Fatalf("code = %s", code)
	}
}

// TestLifecycleReadinessFailureAndRestart verifies status and retry sequencing.
func TestLifecycleReadinessFailureAndRestart(t *testing.T) {
	runner := &fakeRunner{started: make(chan context.Context, 8), ready: true, fail: errors.New("failed")}
	service := newTestService(t, runner, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	stream := newFakeStream(ctx)
	done := make(chan error, 1)
	go func() { done <- service.Manage(stream) }()
	stream.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 7, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING}
	for _, state := range []proto.TunnelStatus_State{proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING, proto.TunnelStatus_TUNNEL_STATUS_STATE_READY, proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING} {
		receiveState(t, stream, state)
	}
	runner.mu.Lock()
	runs := runner.runs
	runner.mu.Unlock()
	if runs < 1 {
		t.Fatal("runner did not run")
	}
	stream.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 8, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED}
	var stopped *proto.TunnelStatus
	for stopped == nil {
		select {
		case message := <-stream.sent:
			if message.State == proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED {
				stopped = message
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for STOPPED")
		}
	}
	if stopped.Revision != 8 {
		t.Fatalf("revision = %d", stopped.Revision)
	}
	cancel()
	close(stream.recv)
	<-done
}

// TestReplacementAndStreamCloseCancel verifies replacement and session cleanup.
func TestReplacementAndStreamCloseCancel(t *testing.T) {
	runner := &fakeRunner{started: make(chan context.Context, 4)}
	service := newTestService(t, runner, time.Millisecond)
	stream := newFakeStream(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Manage(stream) }()
	stream.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 1, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING}
	receiveState(t, stream, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING)
	first := <-runner.started
	stream.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 2, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING}
	receiveState(t, stream, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING)
	second := <-runner.started
	select {
	case <-first.Done():
	case <-time.After(time.Second):
		t.Fatal("replacement did not cancel old runner")
	}
	close(stream.recv)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-second.Done():
	case <-time.After(time.Second):
		t.Fatal("stream close did not cancel runner")
	}
}

// TestCrossSessionOwnership verifies one session cannot replace another's tunnel.
func TestCrossSessionOwnership(t *testing.T) {
	runner := &fakeRunner{started: make(chan context.Context, 2)}
	service := newTestService(t, runner, time.Millisecond)
	first := newFakeStream(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- service.Manage(first) }()
	first.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 1, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING}
	receiveState(t, first, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING)
	second := newFakeStream(context.Background())
	second.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 2, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING}
	if code := status.Code(service.Manage(second)); code != codes.FailedPrecondition {
		t.Fatalf("code = %s", code)
	}
	close(first.recv)
	<-firstDone
}

// TestValidationOfRevisionAndState verifies malformed requests are rejected.
func TestValidationOfRevisionAndState(t *testing.T) {
	service := newTestService(t, &fakeRunner{started: make(chan context.Context, 1)}, time.Millisecond)
	for _, request := range []*proto.TunnelDesiredState{
		{TunnelName: "known", Revision: 0, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING},
		{TunnelName: "known", Revision: 1, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_UNSPECIFIED},
		{TunnelName: "", Revision: 1, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING},
	} {
		stream := newFakeStream(context.Background())
		stream.recv <- request
		if code := status.Code(service.Manage(stream)); code != codes.InvalidArgument {
			t.Fatalf("code = %s", code)
		}
	}
}

// TestReplacementAndStoppedWaitForExit verifies processes never overlap.
func TestReplacementAndStoppedWaitForExit(t *testing.T) {
	runner := &delayedExitRunner{started: make(chan struct{}, 2), exit: make(chan struct{})}
	service := newTestService(t, runner, time.Millisecond)
	stream := newFakeStream(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Manage(stream) }()
	stream.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 1, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING}
	receiveState(t, stream, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING)
	<-runner.started
	stream.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 2, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING}
	select {
	case status := <-stream.sent:
		t.Fatalf("replacement reported %s before old process exited", status.State)
	case <-time.After(20 * time.Millisecond):
	}
	close(runner.exit)
	receiveState(t, stream, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING)
	<-runner.started
	runner.mu.Lock()
	maximum := runner.max
	runner.mu.Unlock()
	if maximum != 1 {
		t.Fatalf("maximum concurrent runners = %d, want 1", maximum)
	}
	stream.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 3, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED}
	receiveState(t, stream, proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED)
	close(stream.recv)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestStreamCloseWaitsAndReconnectAcceptsRevision verifies session-local revisions.
func TestStreamCloseWaitsAndReconnectAcceptsRevision(t *testing.T) {
	runner := &delayedExitRunner{started: make(chan struct{}, 2), exit: make(chan struct{})}
	service := newTestService(t, runner, time.Millisecond)
	first := newFakeStream(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- service.Manage(first) }()
	first.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 9, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING}
	receiveState(t, first, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING)
	<-runner.started
	close(first.recv)
	select {
	case <-firstDone:
		t.Fatal("stream returned before process exited")
	case <-time.After(20 * time.Millisecond):
	}
	close(runner.exit)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}

	second := newFakeStream(context.Background())
	secondDone := make(chan error, 1)
	go func() { secondDone <- service.Manage(second) }()
	second.recv <- &proto.TunnelDesiredState{TunnelName: "known", Revision: 1, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING}
	receiveState(t, second, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING)
	close(second.recv)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

// TestDuplicateStoppedIsIdempotent verifies duplicate requests emit no status.
func TestDuplicateStoppedIsIdempotent(t *testing.T) {
	service := newTestService(t, &fakeRunner{started: make(chan context.Context, 1)}, time.Millisecond)
	stream := newFakeStream(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Manage(stream) }()
	request := &proto.TunnelDesiredState{TunnelName: "known", Revision: 1, State: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED}
	stream.recv <- request
	receiveState(t, stream, proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED)
	stream.recv <- request
	close(stream.recv)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case duplicate := <-stream.sent:
		t.Fatalf("duplicate status sent: %v", duplicate)
	default:
	}
}

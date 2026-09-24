// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/internal/proto"
)

// Runner runs one locally configured tunnel and calls ready once it is usable.
type Runner interface {
	Run(context.Context, string, func()) error
}

// ownedTunnel tracks a tunnel process owned by one control session.
type ownedTunnel struct {
	owner    *session
	revision uint64
	state    proto.TunnelDesiredState_State
	cancel   context.CancelFunc
	done     chan struct{}
}

// session serializes status sends and records its latest desired revisions.
type session struct {
	stream grpc.BidiStreamingServer[proto.TunnelDesiredState, proto.TunnelStatus]
	sendMu sync.Mutex
	latest map[string]desiredRevision
}

// desiredRevision records the latest accepted request for a tunnel.
type desiredRevision struct {
	revision uint64
	state    proto.TunnelDesiredState_State
}

// Service implements the generated local TunnelService.
type Service struct {
	proto.UnimplementedTunnelServiceServer
	runner     Runner
	allowed    map[string]struct{}
	minBackoff time.Duration
	maxBackoff time.Duration

	mu    sync.Mutex
	owned map[string]*ownedTunnel
}

// NewService creates a service restricted to the supplied tunnel names.
func NewService(tunnelNames []string, runner Runner, minBackoff, maxBackoff time.Duration) (*Service, error) {
	if runner == nil {
		return nil, fmt.Errorf("tunnel runner is required")
	}
	if minBackoff <= 0 || maxBackoff < minBackoff {
		return nil, fmt.Errorf("invalid restart backoff")
	}
	allowed := make(map[string]struct{}, len(tunnelNames))
	for _, tunnelName := range tunnelNames {
		if tunnelName == "" {
			return nil, fmt.Errorf("empty tunnel name")
		}
		if _, exists := allowed[tunnelName]; exists {
			return nil, fmt.Errorf("duplicate tunnel name %q", tunnelName)
		}
		allowed[tunnelName] = struct{}{}
	}
	return &Service{runner: runner, allowed: allowed, minBackoff: minBackoff, maxBackoff: maxBackoff, owned: make(map[string]*ownedTunnel)}, nil
}

// Manage owns every tunnel requested on stream until it closes.
func (s *Service) Manage(stream grpc.BidiStreamingServer[proto.TunnelDesiredState, proto.TunnelStatus]) error {
	sess := &session{stream: stream, latest: make(map[string]desiredRevision)}
	defer s.release(sess)
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.apply(sess, req); err != nil {
			return err
		}
	}
}

// apply validates and applies one desired-state request.
func (s *Service) apply(sess *session, req *proto.TunnelDesiredState) error {
	if req == nil || req.TunnelName == "" {
		return status.Error(codes.InvalidArgument, "tunnel name is required")
	}
	if _, ok := s.allowed[req.TunnelName]; !ok {
		return status.Errorf(codes.PermissionDenied, "tunnel %q is not allowed", req.TunnelName)
	}
	if req.Revision == 0 {
		return status.Error(codes.InvalidArgument, "tunnel revision must be non-zero")
	}
	if req.State != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING && req.State != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED {
		return status.Error(codes.InvalidArgument, "tunnel state must be RUNNING or STOPPED")
	}
	latest, seen := sess.latest[req.TunnelName]
	if seen && req.Revision < latest.revision {
		return status.Error(codes.FailedPrecondition, "stale tunnel revision")
	}
	if seen && req.Revision == latest.revision {
		if latest.state == req.State {
			return nil
		}
		return status.Error(codes.FailedPrecondition, "conflicting duplicate tunnel revision")
	}
	s.mu.Lock()
	current := s.owned[req.TunnelName]
	if current != nil && current.owner != sess {
		s.mu.Unlock()
		return status.Error(codes.FailedPrecondition, "tunnel is owned by another session")
	}
	if current != nil && current.cancel != nil {
		current.cancel()
	}
	s.mu.Unlock()
	if current != nil {
		<-current.done
	}
	sess.latest[req.TunnelName] = desiredRevision{revision: req.Revision, state: req.State}
	if req.State == proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED {
		s.mu.Lock()
		if s.owned[req.TunnelName] == current {
			delete(s.owned, req.TunnelName)
		}
		s.mu.Unlock()
		return sess.send(req.TunnelName, req.Revision, proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED, nil)
	}
	ctx, cancel := context.WithCancel(sess.stream.Context())
	t := &ownedTunnel{owner: sess, revision: req.Revision, state: req.State, cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.owned[req.TunnelName] = t
	s.mu.Unlock()
	if err := sess.send(req.TunnelName, req.Revision, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING, nil); err != nil {
		cancel()
		s.mu.Lock()
		if s.owned[req.TunnelName] == t {
			delete(s.owned, req.TunnelName)
		}
		s.mu.Unlock()
		return err
	}
	go s.run(ctx, sess, req.TunnelName, req.Revision, t)
	return nil
}

// run executes and restarts the currently owned tunnel until cancellation.
func (s *Service) run(ctx context.Context, sess *session, tunnelName string, revision uint64, owned *ownedTunnel) {
	defer close(owned.done)
	backoff := s.minBackoff
	for {
		ready := sync.Once{}
		err := s.runner.Run(ctx, tunnelName, func() {
			ready.Do(func() { _ = sess.send(tunnelName, revision, proto.TunnelStatus_TUNNEL_STATUS_STATE_READY, nil) })
		})
		if ctx.Err() != nil {
			return
		}
		_ = sess.send(tunnelName, revision, proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED, err)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if !s.isCurrent(tunnelName, owned) {
			return
		}
		_ = sess.send(tunnelName, revision, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING, nil)
		backoff = min(backoff*2, s.maxBackoff)
	}
}

// isCurrent reports whether tunnel remains the current owner for the named tunnel.
func (s *Service) isCurrent(tunnelName string, tunnel *ownedTunnel) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.owned[tunnelName] == tunnel
}

// release cancels and removes all tunnels owned by sess.
func (s *Service) release(sess *session) {
	tunnels := make([]*ownedTunnel, 0)
	s.mu.Lock()
	for _, tunnel := range s.owned {
		if tunnel.owner == sess {
			tunnel.cancel()
			tunnels = append(tunnels, tunnel)
		}
	}
	s.mu.Unlock()
	for _, tunnel := range tunnels {
		<-tunnel.done
	}
	s.mu.Lock()
	for tunnelName, tunnel := range s.owned {
		if tunnel.owner == sess {
			delete(s.owned, tunnelName)
		}
	}
	s.mu.Unlock()
}

// send serializes one status message on the session stream.
func (s *session) send(tunnelName string, revision uint64, state proto.TunnelStatus_State, runErr error) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	message := &proto.TunnelStatus{TunnelName: tunnelName, Revision: revision, State: state}
	if runErr != nil {
		text := runErr.Error()
		message.Error = &text
	}
	return s.stream.Send(message)
}

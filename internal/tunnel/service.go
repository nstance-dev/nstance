// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/nstance-dev/nstance/v2/internal/proto"
)

const (
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

// Runner reports readiness changes and returns only after the Pod is revoked.
type Runner interface {
	Run(context.Context, string, func(bool)) error
}

// ownedTunnel records one running tunnel and its renewable lease.
type ownedTunnel struct {
	cancel     context.CancelFunc
	done       chan struct{}
	revision   uint64
	generation uint64
	state      proto.TunnelStatus_State
	runErr     error
	wantRun    bool
	stopping   bool
	expires    time.Time
}

// Service follows leased tunnel intent from the local shard leader.
type Service struct {
	socket  string
	runner  Runner
	allowed map[string]struct{}
	logger  *slog.Logger

	mu       sync.Mutex
	owned    map[string]*ownedTunnel
	updates  chan *proto.TunnelStatus
	failures chan error
}

// NewService creates a reconnecting service restricted to configured tunnel names.
func NewService(socket string, tunnelNames []string, runner Runner, logger *slog.Logger) (*Service, error) {
	if socket == "" {
		return nil, fmt.Errorf("server tunnel socket is required")
	}
	if runner == nil {
		return nil, fmt.Errorf("tunnel runner is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
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
	return &Service{
		socket: socket, runner: runner, allowed: allowed, logger: logger,
		owned: make(map[string]*ownedTunnel), updates: make(chan *proto.TunnelStatus, 64),
		failures: make(chan error, 1),
	}, nil
}

// Run reconnects to the local server until ctx is canceled.
func (s *Service) Run(ctx context.Context) error {
	backoff := minBackoff
	var fatal error
	for ctx.Err() == nil {
		err := s.session(ctx)
		if errors.Is(err, ErrDrain) {
			fatal = err
			break
		}
		if ctx.Err() != nil {
			break
		}
		s.logger.Warn("tunnel control session lost", "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
		case fatal = <-s.failures:
		case <-time.After(backoff):
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		break
	}
	return errors.Join(fatal, s.stopAll(), ctx.Err())
}

// session exchanges status and leased desired state over one connection.
func (s *Service) session(ctx context.Context) error {
	conn, err := grpc.NewClient("unix://"+s.socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	stream, err := proto.NewTunnelServiceClient(conn).Manage(ctx)
	if err != nil {
		return err
	}
	received := make(chan struct {
		state *proto.TunnelDesiredState
		err   error
	}, 1)
	go func() {
		for {
			state, recvErr := stream.Recv()
			item := struct {
				state *proto.TunnelDesiredState
				err   error
			}{state: state, err: recvErr}
			select {
			case received <- item:
			case <-ctx.Done():
				return
			}
			if recvErr != nil {
				return
			}
		}
	}()
	for {
		select {
		case err := <-s.failures:
			// Deliver terminal failures before closing the control connection.
			for {
				select {
				case update := <-s.updates:
					if update.State == proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED {
						_ = stream.Send(update)
					}
				default:
					return err
				}
			}
		case <-ctx.Done():
			_ = stream.CloseSend()
			return ctx.Err()
		case item := <-received:
			if item.err != nil {
				if errors.Is(item.err, io.EOF) {
					return io.EOF
				}
				return item.err
			}
			if err := s.apply(item.state); err != nil {
				if errors.Is(err, ErrDrain) {
					message := err.Error()
					_ = stream.Send(&proto.TunnelStatus{TunnelName: item.state.TunnelName, Revision: item.state.Revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED, Error: &message})
				}
				return err
			}
		case update := <-s.updates:
			if err := stream.Send(update); err != nil {
				return err
			}
		}
	}
}

// apply validates and applies one leased desired state.
func (s *Service) apply(request *proto.TunnelDesiredState) error {
	if request == nil || request.TunnelName == "" {
		return fmt.Errorf("tunnel name is required")
	}
	if _, ok := s.allowed[request.TunnelName]; !ok {
		return fmt.Errorf("tunnel %q is not configured", request.TunnelName)
	}
	if request.Revision == 0 {
		return fmt.Errorf("tunnel revision must be non-zero")
	}
	if request.State != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING && request.State != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED {
		return fmt.Errorf("tunnel state must be RUNNING or STOPPED")
	}
	if request.State == proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING && request.LeaseSeconds == 0 {
		return fmt.Errorf("running tunnel requires a positive lease")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	wantRun := request.State == proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING
	expires := time.Now().Add(time.Duration(request.LeaseSeconds) * time.Second)
	current := s.owned[request.TunnelName]
	if current != nil {
		if errors.Is(current.runErr, ErrDrain) {
			return current.runErr
		}
		current.revision = request.Revision
		current.generation++
		current.wantRun = wantRun
		current.expires = expires
		if wantRun {
			s.expire(request.TunnelName, current, current.generation, time.Until(expires))
		} else {
			s.stopLocked(request.TunnelName, current)
		}
		s.emit(statusMessage(request.TunnelName, current))
		return nil
	}

	if !wantRun {
		s.emit(&proto.TunnelStatus{TunnelName: request.TunnelName, Revision: request.Revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED})
		return nil
	}
	s.startLocked(request.TunnelName, request.Revision, expires)
	return nil
}

// startLocked starts a drained tunnel with the remaining lease; s.mu is held.
func (s *Service) startLocked(tunnelName string, revision uint64, expires time.Time) {
	ctx, cancel := context.WithCancel(context.Background())
	owned := &ownedTunnel{
		cancel: cancel, done: make(chan struct{}), revision: revision, generation: 1,
		state: proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING, wantRun: true, expires: expires,
	}
	s.owned[tunnelName] = owned
	s.emit(statusMessage(tunnelName, owned))
	s.expire(tunnelName, owned, owned.generation, time.Until(expires))
	go s.run(ctx, tunnelName, owned)
}

// run executes and restarts one tunnel until cancellation.
func (s *Service) run(ctx context.Context, tunnelName string, owned *ownedTunnel) {
	defer close(owned.done)
	backoff := minBackoff
	for {
		ready := sync.Once{}
		err := s.runner.Run(ctx, tunnelName, func(healthy bool) {
			if healthy {
				ready.Do(func() { s.setStatus(tunnelName, owned, proto.TunnelStatus_TUNNEL_STATUS_STATE_READY, nil) })
			} else {
				s.setStatus(tunnelName, owned, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING, nil)
			}
		})
		if errors.Is(err, ErrDrain) {
			s.setStatus(tunnelName, owned, proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED, err)
			select {
			case s.failures <- err:
			default:
			}
			return
		}
		if ctx.Err() != nil {
			return
		}
		s.setStatus(tunnelName, owned, proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED, err)
		if !wait(ctx, backoff) || !s.isCurrent(tunnelName, owned) {
			return
		}
		s.setStatus(tunnelName, owned, proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING, nil)
		backoff = min(backoff*2, maxBackoff)
	}
}

// expire stops a tunnel if generation is not renewed before duration elapses.
func (s *Service) expire(tunnelName string, owned *ownedTunnel, generation uint64, duration time.Duration) {
	go func() {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		<-timer.C
		s.mu.Lock()
		defer s.mu.Unlock()
		current := s.owned[tunnelName]
		expired := current == owned && current.generation == generation
		if expired {
			owned.wantRun = false
			s.stopLocked(tunnelName, owned)
		}
	}()
}

// stopLocked starts revocation without blocking other tunnels; s.mu is held.
func (s *Service) stopLocked(tunnelName string, owned *ownedTunnel) {
	owned.cancel()
	if owned.stopping || errors.Is(owned.runErr, ErrDrain) {
		return
	}
	owned.stopping = true
	owned.state = proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING
	owned.runErr = nil
	s.emit(statusMessage(tunnelName, owned))
	go s.finishStop(tunnelName, owned)
}

// finishStop acknowledges a drain or starts the latest still-leased replacement.
func (s *Service) finishStop(tunnelName string, owned *ownedTunnel) {
	<-owned.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owned[tunnelName] != owned || errors.Is(owned.runErr, ErrDrain) {
		return
	}
	delete(s.owned, tunnelName)
	if owned.wantRun && time.Now().Before(owned.expires) {
		s.startLocked(tunnelName, owned.revision, owned.expires)
	} else {
		s.emit(&proto.TunnelStatus{TunnelName: tunnelName, Revision: owned.revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED})
	}
}

// stopAll stops all tunnels during supervisor shutdown.
func (s *Service) stopAll() error {
	s.mu.Lock()
	owned := make(map[string]*ownedTunnel, len(s.owned))
	for name, tunnel := range s.owned {
		owned[name] = tunnel
		tunnel.wantRun = false
		s.stopLocked(name, tunnel)
	}
	s.mu.Unlock()
	var err error
	for _, tunnel := range owned {
		<-tunnel.done
		s.mu.Lock()
		if errors.Is(tunnel.runErr, ErrDrain) {
			err = errors.Join(err, tunnel.runErr)
		}
		s.mu.Unlock()
	}
	return err
}

// setStatus stores current status; a draining run can only report fatal failure.
func (s *Service) setStatus(tunnelName string, owned *ownedTunnel, state proto.TunnelStatus_State, runErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owned[tunnelName] != owned || (owned.stopping && !errors.Is(runErr, ErrDrain)) {
		return
	}
	owned.state = state
	owned.runErr = runErr
	s.emit(statusMessage(tunnelName, owned))
}

// statusMessage builds a wire status while the caller holds s.mu.
func statusMessage(tunnelName string, owned *ownedTunnel) *proto.TunnelStatus {
	message := &proto.TunnelStatus{TunnelName: tunnelName, Revision: owned.revision, State: owned.state}
	if owned.runErr != nil {
		text := owned.runErr.Error()
		message.Error = &text
	}
	return message
}

// emit records a status for delivery to the current control session.
func (s *Service) emit(message *proto.TunnelStatus) {
	select {
	case s.updates <- message:
	default:
		s.logger.Warn("dropping stale tunnel status", "tunnel", message.GetTunnelName(), "revision", message.GetRevision())
	}
}

// wait blocks for a backoff delay or context cancellation.
func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// isCurrent reports whether tunnel remains the current run for its name.
func (s *Service) isCurrent(tunnelName string, tunnel *ownedTunnel) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.owned[tunnelName] == tunnel
}

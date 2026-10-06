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

// Runner runs one locally configured tunnel and calls ready once it is usable.
type Runner interface {
	Run(context.Context, string, func()) error
}

// ownedTunnel records one running tunnel and its renewable lease.
type ownedTunnel struct {
	cancel     context.CancelFunc
	done       chan struct{}
	revision   uint64
	generation uint64
	state      proto.TunnelStatus_State
	runErr     error
}

// Service follows leased tunnel intent from the local shard leader.
type Service struct {
	socket  string
	runner  Runner
	allowed map[string]struct{}
	logger  *slog.Logger

	mu      sync.Mutex
	applyMu sync.Mutex
	owned   map[string]*ownedTunnel
	updates chan *proto.TunnelStatus
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
	}, nil
}

// Run reconnects to the local server until ctx is canceled.
func (s *Service) Run(ctx context.Context) error {
	backoff := minBackoff
	for ctx.Err() == nil {
		err := s.session(ctx)
		if ctx.Err() != nil {
			break
		}
		s.logger.Warn("tunnel control session lost", "error", err, "backoff", backoff)
		if !wait(ctx, backoff) {
			break
		}
		backoff = min(backoff*2, maxBackoff)
	}
	s.stopAll()
	return ctx.Err()
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
	seen := make(map[string]uint64)
	for {
		select {
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
			first := seen[item.state.GetTunnelName()] != item.state.GetRevision()
			existing := s.currentStatus(item.state.GetTunnelName()) != nil
			if err := s.apply(item.state); err != nil {
				return err
			}
			seen[item.state.GetTunnelName()] = item.state.GetRevision()
			if first && existing {
				if status := s.currentStatus(item.state.GetTunnelName()); status != nil {
					if err := stream.Send(status); err != nil {
						return err
					}
				}
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
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
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
	current := s.owned[request.TunnelName]
	if current != nil && request.State == proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING {
		current.revision = request.Revision
		current.generation++
		generation := current.generation
		s.mu.Unlock()
		s.expire(request.TunnelName, current, generation, time.Duration(request.LeaseSeconds)*time.Second)
		return nil
	}
	s.mu.Unlock()

	if current != nil {
		s.stop(request.TunnelName, current)
	}
	if request.State == proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED {
		s.emit(&proto.TunnelStatus{TunnelName: request.TunnelName, Revision: request.Revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED})
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	owned := &ownedTunnel{cancel: cancel, done: make(chan struct{}), revision: request.Revision, generation: 1, state: proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING}
	s.mu.Lock()
	s.owned[request.TunnelName] = owned
	s.mu.Unlock()
	s.emit(&proto.TunnelStatus{TunnelName: request.TunnelName, Revision: request.Revision, State: proto.TunnelStatus_TUNNEL_STATUS_STATE_STARTING})
	s.expire(request.TunnelName, owned, owned.generation, time.Duration(request.LeaseSeconds)*time.Second)
	go s.run(ctx, request.TunnelName, owned)
	return nil
}

// run executes and restarts one tunnel until cancellation.
func (s *Service) run(ctx context.Context, tunnelName string, owned *ownedTunnel) {
	defer close(owned.done)
	backoff := minBackoff
	for {
		ready := sync.Once{}
		err := s.runner.Run(ctx, tunnelName, func() {
			ready.Do(func() { s.setStatus(tunnelName, owned, proto.TunnelStatus_TUNNEL_STATUS_STATE_READY, nil) })
		})
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
		s.applyMu.Lock()
		defer s.applyMu.Unlock()
		s.mu.Lock()
		current := s.owned[tunnelName]
		expired := current == owned && current.generation == generation
		s.mu.Unlock()
		if expired {
			s.stop(tunnelName, owned)
		}
	}()
}

// stop cancels one current tunnel and waits for its runner to exit.
func (s *Service) stop(tunnelName string, owned *ownedTunnel) {
	owned.cancel()
	<-owned.done
	s.mu.Lock()
	if s.owned[tunnelName] == owned {
		delete(s.owned, tunnelName)
	}
	s.mu.Unlock()
}

// stopAll stops all tunnels during supervisor shutdown.
func (s *Service) stopAll() {
	s.mu.Lock()
	owned := make(map[string]*ownedTunnel, len(s.owned))
	for name, tunnel := range s.owned {
		owned[name] = tunnel
	}
	s.mu.Unlock()
	for name, tunnel := range owned {
		s.stop(name, tunnel)
	}
}

// setStatus stores and emits a current tunnel status.
func (s *Service) setStatus(tunnelName string, owned *ownedTunnel, state proto.TunnelStatus_State, runErr error) {
	s.mu.Lock()
	if s.owned[tunnelName] != owned {
		s.mu.Unlock()
		return
	}
	owned.state = state
	owned.runErr = runErr
	message := statusMessage(tunnelName, owned)
	s.mu.Unlock()
	s.emit(message)
}

// currentStatus returns the latest status for one running tunnel.
func (s *Service) currentStatus(tunnelName string) *proto.TunnelStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owned := s.owned[tunnelName]; owned != nil {
		return statusMessage(tunnelName, owned)
	}
	return nil
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

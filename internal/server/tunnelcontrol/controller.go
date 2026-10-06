// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnelcontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/v2/internal/proto"
)

const (
	heartbeatInterval = 5 * time.Second
	leaseDuration     = 30 * time.Second
)

// ErrUnavailable indicates that no shard-leader tunnel service is available.
var ErrUnavailable = errors.New("tunnel supervisor unavailable")

// result is a terminal status for one desired-state revision.
type result struct {
	status *proto.TunnelStatus
	err    error
}

// desired records a tunnel's requested state and revision.
type desired struct {
	state    proto.TunnelDesiredState_State
	revision uint64
}

// key identifies one tunnel revision.
type key struct {
	tunnelName string
	revision   uint64
}

// session is the one supervisor connection accepted during a leadership term.
type session struct {
	updates chan struct{}
	cancel  context.CancelFunc
}

// Controller serves leased tunnel intent while this server is shard leader.
type Controller struct {
	proto.UnimplementedTunnelServiceServer

	socket    string
	heartbeat time.Duration
	lease     time.Duration
	logger    *slog.Logger

	mu       sync.Mutex
	desired  map[string]desired
	next     uint64
	results  map[key]result
	waiters  map[key][]chan struct{}
	active   bool
	session  *session
	listener net.Listener
	server   *grpc.Server
}

// New creates a leader-scoped tunnel controller.
func New(socket string, logger *slog.Logger) (*Controller, error) {
	if socket == "" {
		return nil, fmt.Errorf("tunnel control socket is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	return &Controller{
		socket: socket, heartbeat: heartbeatInterval, lease: leaseDuration, logger: logger,
		desired: make(map[string]desired), results: make(map[key]result), waiters: make(map[key][]chan struct{}),
	}, nil
}

// Start binds the tunnel control socket for one leadership term.
func (c *Controller) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active {
		return fmt.Errorf("tunnel controller already started")
	}
	listener, err := listen(c.socket)
	if err != nil {
		return err
	}
	server := grpc.NewServer()
	proto.RegisterTunnelServiceServer(server, c)
	c.active = true
	c.listener = listener
	c.server = server
	go func() {
		if err := server.Serve(listener); err != nil {
			c.logger.Warn("tunnel control server stopped", "error", err)
		}
	}()
	return nil
}

// SetDesired changes a tunnel's desired state and returns its revision.
func (c *Controller) SetDesired(tunnelName string, state proto.TunnelDesiredState_State) (uint64, error) {
	if tunnelName == "" {
		return 0, fmt.Errorf("tunnel name is required")
	}
	if state != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING && state != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED {
		return 0, fmt.Errorf("desired state must be RUNNING or STOPPED")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return 0, ErrUnavailable
	}
	if current, ok := c.desired[tunnelName]; ok && current.state == state {
		previous, completed := c.results[key{tunnelName: tunnelName, revision: current.revision}]
		if !completed || (previous.err == nil && previous.status.GetState() != proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED) {
			return current.revision, nil
		}
	}
	revision := c.nextRevisionLocked()
	c.desired[tunnelName] = desired{state: state, revision: revision}
	c.signalLocked()
	return revision, nil
}

// Wait waits for a revision to become ready, failed, or stopped.
func (c *Controller) Wait(ctx context.Context, tunnelName string, revision uint64) (*proto.TunnelStatus, error) {
	k := key{tunnelName: tunnelName, revision: revision}
	c.mu.Lock()
	if r, ok := c.results[k]; ok {
		c.mu.Unlock()
		return r.status, r.err
	}
	if !c.active {
		c.mu.Unlock()
		return nil, ErrUnavailable
	}
	ch := make(chan struct{})
	c.waiters[k] = append(c.waiters[k], ch)
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		c.removeWaiter(k, ch)
		return nil, ctx.Err()
	case <-ch:
		c.mu.Lock()
		r := c.results[k]
		c.mu.Unlock()
		return r.status, r.err
	}
}

// Stop revokes running tunnels and closes the leadership-term service.
func (c *Controller) Stop() {
	c.mu.Lock()
	if !c.active {
		c.mu.Unlock()
		return
	}
	stops := make([]key, 0, len(c.desired))
	for tunnelName, current := range c.desired {
		if current.state != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING {
			continue
		}
		revision := c.nextRevisionLocked()
		c.desired[tunnelName] = desired{state: proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED, revision: revision}
		stops = append(stops, key{tunnelName: tunnelName, revision: revision})
	}
	c.signalLocked()
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*c.heartbeat)
	for _, stop := range stops {
		_, _ = c.Wait(ctx, stop.tunnelName, stop.revision)
	}
	cancel()

	c.mu.Lock()
	c.active = false
	if c.session != nil {
		c.session.cancel()
		c.session = nil
	}
	server, listener := c.server, c.listener
	c.server = nil
	c.listener = nil
	c.failPendingLocked(ErrUnavailable)
	c.mu.Unlock()
	if server != nil {
		server.Stop()
	}
	if listener != nil {
		_ = listener.Close()
	}
	_ = os.Remove(c.socket)
}

// Manage streams leader intent to the locally connecting tunnel supervisor.
func (c *Controller) Manage(stream grpc.BidiStreamingServer[proto.TunnelStatus, proto.TunnelDesiredState]) error {
	ctx, cancel := context.WithCancel(stream.Context())
	sess := &session{updates: make(chan struct{}, 1), cancel: cancel}
	c.mu.Lock()
	if !c.active {
		c.mu.Unlock()
		cancel()
		return status.Error(codes.FailedPrecondition, "server is not shard leader")
	}
	if c.session != nil {
		c.mu.Unlock()
		cancel()
		return status.Error(codes.AlreadyExists, "tunnel supervisor already connected")
	}
	c.session = sess
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		if c.session == sess {
			c.session = nil
		}
		c.mu.Unlock()
	}()

	received := make(chan result, 1)
	go func() {
		for {
			message, err := stream.Recv()
			select {
			case received <- result{status: message, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(c.heartbeat)
	defer ticker.Stop()
	if err := c.sendDesired(stream, true); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sess.updates:
			if err := c.sendDesired(stream, true); err != nil {
				return err
			}
		case <-ticker.C:
			if err := c.sendDesired(stream, false); err != nil {
				return err
			}
		case item := <-received:
			if item.err != nil {
				if errors.Is(item.err, io.EOF) {
					return nil
				}
				return item.err
			}
			c.recordStatus(item.status)
		}
	}
}

// sendDesired sends every state change or renews every running-state lease.
func (c *Controller) sendDesired(stream grpc.BidiStreamingServer[proto.TunnelStatus, proto.TunnelDesiredState], includeStopped bool) error {
	c.mu.Lock()
	items := make(map[string]desired, len(c.desired))
	for name, item := range c.desired {
		if includeStopped || item.state == proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING {
			items[name] = item
		}
	}
	c.mu.Unlock()
	leaseSeconds := uint32((c.lease + time.Second - 1) / time.Second)
	for name, item := range items {
		message := &proto.TunnelDesiredState{TunnelName: name, Revision: item.revision, State: item.state}
		if item.state == proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING {
			message.LeaseSeconds = leaseSeconds
		}
		if err := stream.Send(message); err != nil {
			return err
		}
	}
	return nil
}

// recordStatus stores terminal statuses and wakes their waiters.
func (c *Controller) recordStatus(message *proto.TunnelStatus) {
	if message == nil || message.TunnelName == "" || message.Revision == 0 {
		return
	}
	c.mu.Lock()
	desired, ok := c.desired[message.TunnelName]
	c.mu.Unlock()
	if !ok || desired.revision != message.Revision {
		return
	}
	switch message.State {
	case proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED:
		c.complete(key{tunnelName: message.TunnelName, revision: message.Revision}, result{status: message})
	case proto.TunnelStatus_TUNNEL_STATUS_STATE_READY:
		if desired.state == proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING {
			c.complete(key{tunnelName: message.TunnelName, revision: message.Revision}, result{status: message})
		}
	case proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED:
		if desired.state == proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED {
			c.complete(key{tunnelName: message.TunnelName, revision: message.Revision}, result{status: message})
		}
	}
}

// complete stores a result once and wakes its waiters.
func (c *Controller) complete(k key, r result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.results[k]; exists {
		return
	}
	c.results[k] = r
	for _, waiter := range c.waiters[k] {
		close(waiter)
	}
	delete(c.waiters, k)
}

// removeWaiter removes one canceled waiter.
func (c *Controller) removeWaiter(k key, ch chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	waiters := c.waiters[k]
	for i, waiter := range waiters {
		if waiter == ch {
			waiters = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(waiters) == 0 {
		delete(c.waiters, k)
	} else {
		c.waiters[k] = waiters
	}
}

// nextRevisionLocked returns the next nonzero revision while c.mu is held.
func (c *Controller) nextRevisionLocked() uint64 {
	c.next++
	if c.next == 0 {
		c.next++
	}
	return c.next
}

// signalLocked wakes the active stream while c.mu is held.
func (c *Controller) signalLocked() {
	if c.session == nil {
		return
	}
	select {
	case c.session.updates <- struct{}{}:
	default:
	}
}

// failPendingLocked completes every pending wait while c.mu is held.
func (c *Controller) failPendingLocked(err error) {
	for k, waiters := range c.waiters {
		if _, complete := c.results[k]; complete {
			continue
		}
		c.results[k] = result{err: err}
		for _, waiter := range waiters {
			close(waiter)
		}
		delete(c.waiters, k)
	}
}

// listen binds a mode-0660 Unix socket in an existing directory.
func listen(path string) (net.Listener, error) {
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("tunnel socket parent directory must exist")
	}
	if existing, statErr := os.Lstat(path); statErr == nil {
		if existing.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refuse to replace non-socket path %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale tunnel socket: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("inspect tunnel socket: %w", statErr)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on tunnel socket: %w", err)
	}
	if err := os.Chmod(path, 0660); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("set tunnel socket permissions: %w", err)
	}
	return listener, nil
}

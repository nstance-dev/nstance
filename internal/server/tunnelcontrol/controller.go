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
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/nstance-dev/nstance/internal/proto"
)

// ErrUnavailable indicates that a requested result was lost with its Manage
// session. Callers may set the desired state again to obtain a new revision.
var ErrUnavailable = errors.New("tunnel supervisor unavailable")

// result is a terminal status or session error for one logical revision.
type result struct {
	status *proto.TunnelStatus
	err    error
}

// desired records a tunnel's requested state and logical revision.
type desired struct {
	state    proto.TunnelDesiredState_State
	revision uint64
}

// key identifies one logical tunnel revision.
type key struct {
	tunnelName string
	revision   uint64
}

// Controller maintains one reconnecting TunnelService Manage session.
type Controller struct {
	socket string
	logger *slog.Logger

	mu      sync.Mutex
	desired map[string]desired
	next    uint64
	results map[key]result
	waiters map[key][]chan struct{}
	updates chan struct{}
	started bool
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{}
	conn    *grpc.ClientConn
}

// New creates a controller for a Unix socket.
func New(socket string, logger *slog.Logger) (*Controller, error) {
	if socket == "" {
		return nil, fmt.Errorf("tunnel supervisor socket is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	return &Controller{
		socket: socket, logger: logger, desired: make(map[string]desired),
		results: make(map[key]result), waiters: make(map[key][]chan struct{}),
		updates: make(chan struct{}, 1), done: make(chan struct{}),
	}, nil
}

// Start starts the reconnect loop. It may only be called once.
func (c *Controller) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return fmt.Errorf("tunnel controller already started")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.started = true
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	go c.run(runCtx)
	return nil
}

// SetDesired changes a tunnel's desired state and returns its logical
// revision. Repeating the current state returns the existing revision.
func (c *Controller) SetDesired(tunnelName string, state proto.TunnelDesiredState_State) (uint64, error) {
	if tunnelName == "" {
		return 0, fmt.Errorf("tunnel name is required")
	}
	if state != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING && state != proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED {
		return 0, fmt.Errorf("desired state must be RUNNING or STOPPED")
	}
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return 0, ErrUnavailable
	}
	if current, ok := c.desired[tunnelName]; ok && current.state == state {
		previous, completed := c.results[key{tunnelName: tunnelName, revision: current.revision}]
		if !completed || previous.err == nil {
			c.mu.Unlock()
			return current.revision, nil
		}
	}
	c.next++
	if c.next == 0 {
		c.next++
	}
	revision := c.next
	c.desired[tunnelName] = desired{state: state, revision: revision}
	c.mu.Unlock()
	c.signalUpdate()
	return revision, nil
}

// Wait waits for the exact logical revision to become READY, FAILED, or
// STOPPED. A session loss fails pending waits rather than silently moving them
// to a different supervisor session.
func (c *Controller) Wait(ctx context.Context, tunnelName string, revision uint64) (*proto.TunnelStatus, error) {
	k := key{tunnelName: tunnelName, revision: revision}
	c.mu.Lock()
	if r, ok := c.results[k]; ok {
		c.mu.Unlock()
		return r.status, r.err
	}
	if c.stopped {
		c.mu.Unlock()
		return nil, ErrUnavailable
	}
	ch := make(chan struct{})
	c.waiters[k] = append(c.waiters[k], ch)
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		c.mu.Lock()
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
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-ch:
		c.mu.Lock()
		r := c.results[k]
		c.mu.Unlock()
		return r.status, r.err
	}
}

// Stop closes the active session and connection and waits for the reconnect
// goroutine to exit. It is safe to call more than once.
func (c *Controller) Stop() {
	c.mu.Lock()
	if !c.started {
		c.stopped = true
		c.failPendingLocked(ErrUnavailable)
		c.mu.Unlock()
		return
	}
	cancel, done := c.cancel, c.done
	c.mu.Unlock()
	cancel()
	<-done
}

// run reconnects control sessions until ctx is canceled.
func (c *Controller) run(ctx context.Context) {
	defer close(c.done)
	defer func() {
		c.mu.Lock()
		c.stopped = true
		c.failPendingLocked(ErrUnavailable)
		c.mu.Unlock()
	}()
	backoff := 20 * time.Millisecond
	for ctx.Err() == nil {
		err := c.session(ctx)
		if ctx.Err() != nil {
			break
		}
		c.logger.Warn("tunnel supervisor session lost", "error", err, "backoff", backoff)
		c.mu.Lock()
		c.failPendingLocked(fmt.Errorf("%w: %v", ErrUnavailable, err))
		c.mu.Unlock()
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		backoff = min(backoff*2, time.Second)
	}
	c.mu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()
}

// session exchanges desired states and statuses over one connection.
func (c *Controller) session(ctx context.Context) (sessionErr error) {
	conn, err := grpc.NewClient("unix://"+c.socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		_ = conn.Close()
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
	}()
	stream, err := proto.NewTunnelServiceClient(conn).Manage(ctx)
	if err != nil {
		return err
	}

	// Wire revisions are deliberately local to this session. The map translates
	// terminal statuses back to the stable logical revisions exposed to callers.
	wire := uint64(0)
	sent := make(map[string]desired)
	logical := make(map[key]uint64)
	defer func() {
		if sessionErr == nil || ctx.Err() != nil {
			return
		}
		availabilityErr := fmt.Errorf("%w: %v", ErrUnavailable, sessionErr)
		for wireKey, logicalRevision := range logical {
			c.complete(key{tunnelName: wireKey.tunnelName, revision: logicalRevision}, result{err: availabilityErr})
		}
	}()
	sendCurrent := func() error {
		c.mu.Lock()
		copyDesired := make(map[string]desired, len(c.desired))
		for tunnelName, d := range c.desired {
			copyDesired[tunnelName] = d
		}
		c.mu.Unlock()
		for tunnelName, d := range copyDesired {
			if old, ok := sent[tunnelName]; ok && old == d {
				continue
			}
			wire++
			if err := stream.Send(&proto.TunnelDesiredState{TunnelName: tunnelName, Revision: wire, State: d.state}); err != nil {
				return err
			}
			sent[tunnelName] = d
			logical[key{tunnelName: tunnelName, revision: wire}] = d.revision
		}
		return nil
	}
	if err := sendCurrent(); err != nil {
		return err
	}
	recv := make(chan result, 1)
	go func() {
		message, recvErr := stream.Recv()
		recv <- result{status: message, err: recvErr}
	}()
	for {
		select {
		case <-ctx.Done():
			_ = stream.CloseSend()
			return ctx.Err()
		case <-c.updates:
			if err := sendCurrent(); err != nil {
				return err
			}
		case received := <-recv:
			if received.err != nil {
				if errors.Is(received.err, io.EOF) {
					return io.EOF
				}
				return received.err
			}
			message := received.status
			logicalRevision, ok := logical[key{tunnelName: message.TunnelName, revision: message.Revision}]
			switch message.State {
			case proto.TunnelStatus_TUNNEL_STATUS_STATE_READY,
				proto.TunnelStatus_TUNNEL_STATUS_STATE_FAILED,
				proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED:
				if !ok {
					break
				}
				copyStatus := &proto.TunnelStatus{
					TunnelName: message.TunnelName,
					Revision:   logicalRevision,
					State:      message.State,
					Error:      message.Error,
				}
				c.complete(key{tunnelName: message.TunnelName, revision: logicalRevision}, result{status: copyStatus})
			}
			go func() {
				message, recvErr := stream.Recv()
				recv <- result{status: message, err: recvErr}
			}()
		}
	}
}

// signalUpdate notifies the active session without blocking the caller.
func (c *Controller) signalUpdate() {
	select {
	case c.updates <- struct{}{}:
	default:
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

// failPendingLocked completes all pending waits with err while c.mu is held.
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

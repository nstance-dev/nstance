// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/nstance-dev/nstance/pkg/proxy"
)

// Options configures a proxy server.
type Options struct {
	Config          proxy.Config
	Waker           Waker
	HoldTimeout     time.Duration
	DialTimeout     time.Duration
	ShutdownTimeout time.Duration
	BindHost        string
	Logger          *slog.Logger
}

// listenerRoute is the complete immutable route selected for a connection.
type listenerRoute struct {
	identity string
}

// routingSnapshot is the immutable routing generation for one proxy port.
type routingSnapshot struct {
	exclusive *listenerRoute
	byIP      map[string]*listenerRoute
}

// portListeners describes routing and the bound socket for one proxy port.
type portListeners struct {
	mu       sync.RWMutex
	routes   *routingSnapshot
	listener net.Listener
}

// Reconcile replaces routing in place while retaining sockets for unchanged ports.
func (s *Server) Reconcile(config proxy.Config) error {
	candidate, err := New(Options{Config: config, Waker: s.waker, HoldTimeout: s.holdTimeout, DialTimeout: s.dialTimeout, ShutdownTimeout: s.shutdownTimeout, BindHost: s.bindHost, Logger: s.logger})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return fmt.Errorf("proxy server is not started")
	}
	added := make(map[int]net.Listener)
	for port := range candidate.ports {
		if s.ports[port] != nil {
			continue
		}
		listener, err := net.Listen("tcp", net.JoinHostPort(s.bindHost, strconv.Itoa(port)))
		if err != nil {
			for _, item := range added {
				_ = item.Close()
			}
			return fmt.Errorf("listen on proxy port %d: %w", port, err)
		}
		added[port] = listener
	}
	for port, current := range s.ports {
		next := candidate.ports[port]
		if next == nil {
			_ = current.listener.Close()
			delete(s.ports, port)
			continue
		}
		current.mu.Lock()
		current.routes = next.routes
		current.mu.Unlock()
		delete(candidate.ports, port)
	}
	for port, next := range candidate.ports {
		next.listener = added[port]
		s.ports[port] = next
		s.acceptWG.Add(1)
		go s.acceptLoop(s.ctx, next)
	}
	s.config = config
	return nil
}

// Server accepts health checks without waking and wakes only after payload arrival.
type Server struct {
	config          proxy.Config
	waker           Waker
	holdTimeout     time.Duration
	dialTimeout     time.Duration
	shutdownTimeout time.Duration
	bindHost        string
	logger          *slog.Logger

	mu            sync.Mutex
	ports         map[int]*portListeners
	started       bool
	closeOnce     sync.Once
	connections   map[net.Conn]struct{}
	ctx           context.Context
	cancel        context.CancelFunc
	acceptWG      sync.WaitGroup
	handlerWG     sync.WaitGroup
	routeSelected func()
}

// New creates and validates a proxy server.
func New(opts Options) (*Server, error) {
	if opts.Waker == nil {
		return nil, fmt.Errorf("waker is required")
	}
	if opts.HoldTimeout <= 0 {
		return nil, fmt.Errorf("hold timeout must be positive")
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 10 * time.Second
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	server := &Server{
		config:          opts.Config,
		waker:           opts.Waker,
		holdTimeout:     opts.HoldTimeout,
		dialTimeout:     opts.DialTimeout,
		shutdownTimeout: opts.ShutdownTimeout,
		bindHost:        opts.BindHost,
		logger:          opts.Logger,
		ports:           make(map[int]*portListeners),
		connections:     make(map[net.Conn]struct{}),
	}
	for key, listener := range opts.Config.Listeners {
		if listener.ProxyPort < 1 || listener.ProxyPort > 65535 {
			return nil, fmt.Errorf("listener %s has invalid proxy port %d", key, listener.ProxyPort)
		}
		port := server.ports[listener.ProxyPort]
		if port == nil {
			port = &portListeners{routes: &routingSnapshot{byIP: make(map[string]*listenerRoute)}}
			server.ports[listener.ProxyPort] = port
		}
		listener.Groups = append([]string(nil), listener.Groups...)
		route := &listenerRoute{identity: key}
		if listener.DestinationIP == "" {
			if port.routes.exclusive != nil || len(port.routes.byIP) > 0 {
				return nil, fmt.Errorf("proxy port %d is not exclusively owned by %s", listener.ProxyPort, key)
			}
			port.routes.exclusive = route
			continue
		}
		if port.routes.exclusive != nil {
			return nil, fmt.Errorf("proxy port %d mixes exclusive and destination listeners", listener.ProxyPort)
		}
		ip := net.ParseIP(listener.DestinationIP)
		if ip == nil {
			return nil, fmt.Errorf("listener %s has invalid destination IP %q", key, listener.DestinationIP)
		}
		normalized := ip.String()
		if previous := port.routes.byIP[normalized]; previous != nil {
			return nil, fmt.Errorf("destination %s:%d is used by %s and %s", normalized, listener.ProxyPort, previous.identity, key)
		}
		port.routes.byIP[normalized] = route
	}
	return server, nil
}

// Start binds all configured ports and serves until the context is canceled.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("proxy server already started")
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	for port, listeners := range s.ports {
		address := net.JoinHostPort(s.bindHost, strconv.Itoa(port))
		listener, err := net.Listen("tcp", address)
		if err != nil {
			s.closeListeners()
			return fmt.Errorf("listen on %s: %w", address, err)
		}
		listeners.listener = listener
		s.acceptWG.Add(1)
		go s.acceptLoop(s.ctx, listeners)
	}
	s.started = true
	go func() {
		<-s.ctx.Done()
		_ = s.Close()
	}()
	return nil
}

// Close stops accepting connections and waits for active connections to drain.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.started = false
		if s.cancel != nil {
			s.cancel()
		}
		s.closeListeners()
	})
	s.acceptWG.Wait()
	done := make(chan struct{})
	go func() {
		s.handlerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(s.shutdownTimeout):
		s.closeConnections()
	}
	return nil
}

// closeListeners closes every bound proxy listener.
func (s *Server) closeListeners() {
	for _, listeners := range s.ports {
		if listeners.listener != nil {
			_ = listeners.listener.Close()
		}
	}
}

// trackConnection adds a connection to the shutdown set.
func (s *Server) trackConnection(connection net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections[connection] = struct{}{}
}

// untrackConnection removes a connection from the shutdown set.
func (s *Server) untrackConnection(connection net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.connections, connection)
}

// closeConnections forces every active connection closed.
func (s *Server) closeConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for connection := range s.connections {
		_ = connection.Close()
	}
}

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

// portListeners describes routing and the bound socket for one proxy port.
type portListeners struct {
	exclusiveKey string
	byIP         map[string]string
	listener     net.Listener
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

	mu          sync.Mutex
	ports       map[int]*portListeners
	started     bool
	closeOnce   sync.Once
	connections map[net.Conn]struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	acceptWG    sync.WaitGroup
	handlerWG   sync.WaitGroup
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
			port = &portListeners{byIP: make(map[string]string)}
			server.ports[listener.ProxyPort] = port
		}
		if listener.DestinationIP == "" {
			if port.exclusiveKey != "" || len(port.byIP) > 0 {
				return nil, fmt.Errorf("proxy port %d is not exclusively owned by %s", listener.ProxyPort, key)
			}
			port.exclusiveKey = key
			continue
		}
		if port.exclusiveKey != "" {
			return nil, fmt.Errorf("proxy port %d mixes exclusive and destination listeners", listener.ProxyPort)
		}
		ip := net.ParseIP(listener.DestinationIP)
		if ip == nil {
			return nil, fmt.Errorf("listener %s has invalid destination IP %q", key, listener.DestinationIP)
		}
		normalized := ip.String()
		if previous := port.byIP[normalized]; previous != "" {
			return nil, fmt.Errorf("destination %s:%d is used by %s and %s", normalized, listener.ProxyPort, previous, key)
		}
		port.byIP[normalized] = key
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

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package wake

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/grpc"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/pkg/proxy"
)

// Handler handles the sole operation exposed over the local wake socket.
type Handler interface {
	WakeListener(context.Context, string) (*proto.WakeTenantResponse, error)
}

// wakeService adapts Handler to the generated operator gRPC service.
type wakeService struct {
	proto.UnimplementedProxyServiceServer
	handler Handler
	server  *Server
}

// WatchConfig sends the current proxy listener configuration and each replacement.
func (s *wakeService) WatchConfig(_ *proto.WatchProxyConfigRequest, stream proto.ProxyService_WatchConfigServer) error {
	subscriber, snapshot := s.server.subscribe()
	defer s.server.unsubscribe(subscriber)
	for {
		if err := stream.Send(snapshot); err != nil {
			return err
		}
		select {
		case snapshot = <-subscriber:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// WakeTenant forwards a local wake request to the configured handler.
func (s *wakeService) WakeTenant(ctx context.Context, request *proto.ProxyWakeRequest) (*proto.WakeTenantResponse, error) {
	return s.handler.WakeListener(ctx, request.GetListener())
}

// Server owns the root-controlled Unix wake socket.
type Server struct {
	path    string
	handler Handler

	mu          sync.Mutex
	listener    net.Listener
	grpc        *grpc.Server
	generation  uint64
	snapshot    *proto.ProxyConfigSnapshot
	subscribers map[chan *proto.ProxyConfigSnapshot]struct{}
}

// New creates a local wake server.
func New(path string, handler Handler) (*Server, error) {
	if path == "" {
		return nil, fmt.Errorf("wake socket path is required")
	}
	if handler == nil {
		return nil, fmt.Errorf("wake handler is required")
	}
	return &Server{path: path, handler: handler, snapshot: snapshotFromConfig(0, proxy.Config{Listeners: map[string]proxy.Listener{}}), subscribers: make(map[chan *proto.ProxyConfigSnapshot]struct{})}, nil
}

// Publish atomically replaces the configuration delivered to all subscribers.
func (s *Server) Publish(config proxy.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.snapshot = snapshotFromConfig(s.generation, config)
	for subscriber := range s.subscribers {
		select {
		case <-subscriber:
		default:
		}
		select {
		case subscriber <- s.snapshot:
		default:
		}
	}
}

// subscribe registers a replacement snapshot channel and returns the current snapshot.
func (s *Server) subscribe() (chan *proto.ProxyConfigSnapshot, *proto.ProxyConfigSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan *proto.ProxyConfigSnapshot, 1)
	s.subscribers[ch] = struct{}{}
	return ch, s.snapshot
}

// unsubscribe removes a snapshot channel.
func (s *Server) unsubscribe(ch chan *proto.ProxyConfigSnapshot) {
	s.mu.Lock()
	delete(s.subscribers, ch)
	s.mu.Unlock()
}

// snapshotFromConfig converts the internal configuration to its wire representation.
func snapshotFromConfig(generation uint64, cfg proxy.Config) *proto.ProxyConfigSnapshot {
	result := &proto.ProxyConfigSnapshot{Generation: generation, Listeners: make(map[string]*proto.ProxyListener, len(cfg.Listeners))}
	for key, item := range cfg.Listeners {
		listener := &proto.ProxyListener{Tenant: item.Tenant, Groups: append([]string(nil), item.Groups...), TargetPort: uint32(item.TargetPort), ProxyPort: uint32(item.ProxyPort)}
		if item.DestinationIP != "" {
			listener.DestinationIp = &item.DestinationIP
		}
		result.Listeners[key] = listener
	}
	return result
}

// Start binds a fresh mode-0660 Unix socket and begins serving.
func (s *Server) Start(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return fmt.Errorf("wake server already started")
	}
	directory := filepath.Dir(s.path)
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("proxy socket parent directory must exist")
	}
	if info, err := os.Lstat(s.path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refuse to replace non-socket wake path %s", s.path)
		}
		if err := os.Remove(s.path); err != nil {
			return fmt.Errorf("remove stale wake socket: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect stale wake socket: %w", err)
	}
	listener, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("listen on wake socket: %w", err)
	}
	if err := os.Chmod(s.path, 0660); err != nil {
		_ = listener.Close()
		return fmt.Errorf("set wake socket mode: %w", err)
	}
	server := grpc.NewServer()
	proto.RegisterProxyServiceServer(server, &wakeService{handler: s.handler, server: s})
	s.listener = listener
	s.grpc = server
	go func() {
		_ = server.Serve(listener)
	}()
	return nil
}

// Stop immediately closes the local API and removes its socket.
func (s *Server) Stop() {
	s.mu.Lock()
	server, listener := s.grpc, s.listener
	s.grpc = nil
	s.listener = nil
	s.mu.Unlock()
	if server != nil {
		server.Stop()
	}
	if listener != nil {
		_ = listener.Close()
	}
	_ = os.Remove(s.path)
}

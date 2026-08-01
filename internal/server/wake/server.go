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
	"github.com/nstance-dev/nstance/internal/proxy/config"
)

// Handler handles the sole operation exposed over the local wake socket.
type Handler interface {
	WakeListener(context.Context, *proto.WakeTenantRequest) (*proto.WakeTenantResponse, error)
}

// wakeService adapts Handler to the generated operator gRPC service.
type wakeService struct {
	proto.UnimplementedOperatorServiceServer
	handler Handler
}

// WakeTenant forwards a local wake request to the configured handler.
func (s *wakeService) WakeTenant(ctx context.Context, request *proto.WakeTenantRequest) (*proto.WakeTenantResponse, error) {
	return s.handler.WakeListener(ctx, request)
}

// Server owns the root-controlled Unix wake socket.
type Server struct {
	path    string
	uid     int
	gid     int
	handler Handler

	mu       sync.Mutex
	listener net.Listener
	grpc     *grpc.Server
}

// New creates a local wake server.
func New(path string, uid, gid int, handler Handler) (*Server, error) {
	if path == "" {
		return nil, fmt.Errorf("wake socket path is required")
	}
	if handler == nil {
		return nil, fmt.Errorf("wake handler is required")
	}
	return &Server{path: path, uid: uid, gid: gid, handler: handler}, nil
}

// Start binds a fresh mode-0660 Unix socket and begins serving.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return fmt.Errorf("wake server already started")
	}
	if err := config.PrepareRuntimeDirectory(filepath.Dir(s.path), s.uid, s.gid); err != nil {
		return err
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
	if err := os.Chown(s.path, s.uid, s.gid); err != nil {
		_ = listener.Close()
		return fmt.Errorf("set wake socket ownership: %w", err)
	}
	server := grpc.NewServer()
	proto.RegisterOperatorServiceServer(server, &wakeService{handler: s.handler})
	s.listener = listener
	s.grpc = server
	go func() {
		_ = server.Serve(listener)
	}()
	go func() {
		<-ctx.Done()
		s.Stop()
	}()
	return nil
}

// Stop immediately closes the local API and removes its socket.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grpc != nil {
		s.grpc.Stop()
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.grpc = nil
	s.listener = nil
	_ = os.Remove(s.path)
}

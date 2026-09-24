// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"

	"github.com/nstance-dev/nstance/internal/proto"
)

// Serve binds a mode-0660 Unix socket in an existing deployment-owned directory.
func Serve(ctx context.Context, path string, service *Service) error {
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || !info.IsDir() {
		return fmt.Errorf("tunnel socket parent directory must exist")
	}
	if existing, statErr := os.Lstat(path); statErr == nil {
		if existing.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refuse to replace non-socket path %s", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen on tunnel socket: %w", err)
	}
	defer func() { _ = listener.Close() }()
	defer func() { _ = os.Remove(path) }()
	if err := os.Chmod(path, 0660); err != nil {
		return fmt.Errorf("set tunnel socket permissions: %w", err)
	}
	server := grpc.NewServer(grpc.WaitForHandlers(true))
	proto.RegisterTunnelServiceServer(server, service)
	go func() { <-ctx.Done(); server.Stop() }()
	if err := server.Serve(listener); err != nil && ctx.Err() == nil {
		return fmt.Errorf("serve tunnel socket: %w", err)
	}
	return nil
}

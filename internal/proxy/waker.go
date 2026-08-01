// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/nstance-dev/nstance/internal/proto"
)

// Waker wakes a listener's tenant and returns its ready private upstream.
type Waker interface {
	Wake(ctx context.Context, listener, tenant string) (string, error)
}

// UnixWaker invokes listener-scoped WakeTenant over the local root-owned socket.
type UnixWaker struct {
	connection *grpc.ClientConn
	client     proto.OperatorServiceClient
}

// NewUnixWaker connects to the nstance-server local wake socket.
func NewUnixWaker(socketPath string) (*UnixWaker, error) {
	connection, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("connect wake socket: %w", err)
	}
	return &UnixWaker{connection: connection, client: proto.NewOperatorServiceClient(connection)}, nil
}

// Wake requests a listener-scoped wake and requires a ready upstream.
func (w *UnixWaker) Wake(ctx context.Context, listener, tenant string) (string, error) {
	response, err := w.client.WakeTenant(ctx, &proto.WakeTenantRequest{Tenant: tenant, Listener: &listener})
	if err != nil {
		return "", err
	}
	if response.Upstream == nil || response.GetUpstream() == "" {
		return "", fmt.Errorf("wake response did not include an upstream")
	}
	return response.GetUpstream(), nil
}

// Close closes the local wake connection.
func (w *UnixWaker) Close() error { return w.connection.Close() }

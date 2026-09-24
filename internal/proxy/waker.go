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
	"github.com/nstance-dev/nstance/pkg/proxy"
)

// Waker wakes the tenant mapped to a listener and returns its ready upstream.
type Waker interface {
	Wake(ctx context.Context, listener string) (string, error)
}

// UnixWaker invokes listener-scoped WakeTenant over the local root-owned socket.
type UnixWaker struct {
	connection *grpc.ClientConn
	client     proto.ProxyServiceClient
}

// NewUnixWaker connects to the nstance-server local wake socket.
func NewUnixWaker(socketPath string) (*UnixWaker, error) {
	connection, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("connect wake socket: %w", err)
	}
	return &UnixWaker{connection: connection, client: proto.NewProxyServiceClient(connection)}, nil
}

// WatchConfig watches complete replacement snapshots until the connection fails.
func (w *UnixWaker) WatchConfig(ctx context.Context, apply func(proxy.Config) error) error {
	stream, err := w.client.WatchConfig(ctx, &proto.WatchProxyConfigRequest{})
	if err != nil {
		return err
	}
	return watchConfigSnapshots(stream, apply)
}

// configSnapshotReceiver receives complete proxy configuration snapshots.
type configSnapshotReceiver interface {
	Recv() (*proto.ProxyConfigSnapshot, error)
}

// watchConfigSnapshots validates snapshot ordering and applies each replacement.
func watchConfigSnapshots(stream configSnapshotReceiver, apply func(proxy.Config) error) error {
	var previous uint64
	received := false
	for {
		snapshot, err := stream.Recv()
		if err != nil {
			return err
		}
		generation := snapshot.GetGeneration()
		if received && generation == 0 {
			return fmt.Errorf("proxy config generation is zero after initial publication")
		}
		if received && generation <= previous {
			return fmt.Errorf("proxy config generation %d is not greater than %d", generation, previous)
		}
		cfg := proxy.Config{Listeners: make(map[string]proxy.Listener, len(snapshot.Listeners))}
		for key, item := range snapshot.Listeners {
			cfg.Listeners[key] = proxy.Listener{Tenant: item.Tenant, Groups: append([]string(nil), item.Groups...), TargetPort: int(item.TargetPort), ProxyPort: int(item.ProxyPort), DestinationIP: item.GetDestinationIp()}
		}
		if err := apply(cfg); err != nil {
			return err
		}
		previous = generation
		received = true
	}
}

// Wake requests a listener-scoped wake and requires a ready upstream.
func (w *UnixWaker) Wake(ctx context.Context, listener string) (string, error) {
	response, err := w.client.WakeTenant(ctx, &proto.ProxyWakeRequest{Listener: listener})
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

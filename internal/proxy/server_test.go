// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/pkg/proxy"
)

// testWaker delegates wake requests to a test callback.
type testWaker struct {
	wake func(context.Context, string, string) (string, error)
}

// Wake invokes the configured test callback.
func (w testWaker) Wake(ctx context.Context, listener, tenant string) (string, error) {
	return w.wake(ctx, listener, tenant)
}

// TestServerHealthCheckDoesNotWakeAndPayloadForwards verifies probe and payload handling.
func TestServerHealthCheckDoesNotWakeAndPayloadForwards(t *testing.T) {
	upstream, closeUpstream := startEchoServer(t)
	defer closeUpstream()
	port := unusedPort(t)
	var wakes atomic.Int32
	server, err := New(Options{
		Config: proxy.Config{Listeners: map[string]proxy.Listener{
			"api:proxy": {Tenant: "red", ProxyPort: port, TargetPort: 6443},
		}},
		Waker: testWaker{wake: func(_ context.Context, listener, tenant string) (string, error) {
			if listener != "api:proxy" || tenant != "red" {
				t.Errorf("Wake(%q, %q)", listener, tenant)
			}
			wakes.Add(1)
			return upstream, nil
		}},
		HoldTimeout: time.Second,
		BindHost:    "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = server.Close() }()

	health, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("health dial: %v", err)
	}
	_ = health.Close()
	time.Sleep(20 * time.Millisecond)
	if wakes.Load() != 0 {
		t.Fatalf("health check caused %d wakes", wakes.Load())
	}

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("payload dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	response := make([]byte, 5)
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(response) != "hello" || wakes.Load() != 1 {
		t.Fatalf("response %q, wakes %d", response, wakes.Load())
	}
}

// TestServerDispatchesSharedPortByDestinationIP verifies destination-based listener dispatch.
func TestServerDispatchesSharedPortByDestinationIP(t *testing.T) {
	upstream, closeUpstream := startEchoServer(t)
	defer closeUpstream()
	port := unusedPort(t)
	var mu sync.Mutex
	var identities []string
	server, err := New(Options{
		Config: proxy.Config{Listeners: map[string]proxy.Listener{
			"one": {Tenant: "red", ProxyPort: port, TargetPort: port, DestinationIP: "127.0.0.1"},
			"two": {Tenant: "red", ProxyPort: port, TargetPort: port, DestinationIP: "127.0.0.2"},
		}},
		Waker: testWaker{wake: func(_ context.Context, listener, _ string) (string, error) {
			mu.Lock()
			identities = append(identities, listener)
			mu.Unlock()
			return upstream, nil
		}},
		HoldTimeout: time.Second,
		BindHost:    "0.0.0.0",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = server.Close() }()
	client, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := client.Write([]byte("payload")); err != nil {
		t.Fatalf("write: %v", err)
	}
	response := make([]byte, len("payload"))
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = client.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(identities) != 1 || identities[0] != "one" {
		t.Fatalf("wake identities = %v", identities)
	}
}

// TestServerBoundsConnectionHoldTimeout verifies held connections time out.
func TestServerBoundsConnectionHoldTimeout(t *testing.T) {
	port := unusedPort(t)
	server, err := New(Options{
		Config: proxy.Config{Listeners: map[string]proxy.Listener{
			"api": {Tenant: "red", ProxyPort: port, TargetPort: 6443},
		}},
		Waker: testWaker{wake: func(ctx context.Context, _, _ string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}},
		HoldTimeout: 40 * time.Millisecond,
		BindHost:    "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = server.Close() }()
	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	start := time.Now()
	if _, err := client.Write([]byte("wake")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if _, err := client.Read(buffer); err == nil {
		t.Fatal("connection remained open without an upstream")
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("connection held for %v", elapsed)
	}
}

// TestServerForcesIdleConnectionsClosedAtShutdownDeadline verifies bounded shutdown.
func TestServerForcesIdleConnectionsClosedAtShutdownDeadline(t *testing.T) {
	port := unusedPort(t)
	server, err := New(Options{
		Config: proxy.Config{Listeners: map[string]proxy.Listener{
			"api": {Tenant: "red", ProxyPort: port, TargetPort: 6443},
		}},
		Waker:           testWaker{wake: func(context.Context, string, string) (string, error) { return "", nil }},
		HoldTimeout:     time.Minute,
		ShutdownTimeout: 30 * time.Millisecond,
		BindHost:        "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	deadline := time.Now().Add(time.Second)
	for {
		server.mu.Lock()
		tracked := len(server.connections) > 0
		server.mu.Unlock()
		if tracked || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown took %v", elapsed)
	}
}

// TestServerCloseIsBoundedWhenWakerIgnoresContext verifies shutdown does not wait indefinitely.
func TestServerCloseIsBoundedWhenWakerIgnoresContext(t *testing.T) {
	port := unusedPort(t)
	blocked := make(chan struct{})
	server, err := New(Options{
		Config: proxy.Config{Listeners: map[string]proxy.Listener{
			"api": {Tenant: "red", ProxyPort: port, TargetPort: 6443},
		}},
		Waker: testWaker{wake: func(context.Context, string, string) (string, error) {
			<-blocked
			return "", nil
		}},
		HoldTimeout:     time.Minute,
		ShutdownTimeout: 30 * time.Millisecond,
		BindHost:        "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Write([]byte("wake")); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	start := time.Now()
	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Close took %v", elapsed)
	}
	close(blocked)
}

// unusedPort reserves and releases a TCP port for a test.
func unusedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

// startEchoServer starts a TCP echo server and returns its address and close function.
func startEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start upstream: %v", err)
	}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = connection.Close() }()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	return listener.Addr().String(), func() {
		_ = listener.Close()
	}
}

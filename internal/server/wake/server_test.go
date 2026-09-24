// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package wake

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/proxy"
	proxyconfig "github.com/nstance-dev/nstance/pkg/proxy"
)

// testHandler records local wake requests.
type testHandler struct {
	listener string
}

// TestWatchConfigInitialSnapshotAndCoalescing verifies latest-only publication.
func TestWatchConfigInitialSnapshotAndCoalescing(t *testing.T) {
	server, err := New(filepath.Join(t.TempDir(), "control.sock"), &testHandler{})
	if err != nil {
		t.Fatal(err)
	}
	server.Publish(proxyconfig.Config{Listeners: map[string]proxyconfig.Listener{"first": {Tenant: "red", ProxyPort: 1001}}})
	subscriber, initial := server.subscribe()
	defer server.unsubscribe(subscriber)
	if initial.Generation != 1 || initial.Listeners["first"].Tenant != "red" {
		t.Fatalf("initial snapshot = %#v", initial)
	}
	server.Publish(proxyconfig.Config{Listeners: map[string]proxyconfig.Listener{"second": {Tenant: "blue", ProxyPort: 1002}}})
	server.Publish(proxyconfig.Config{Listeners: map[string]proxyconfig.Listener{"latest": {Tenant: "green", ProxyPort: 1003}}})
	select {
	case got := <-subscriber:
		if got.Generation != 3 || got.Listeners["latest"] == nil || len(got.Listeners) != 1 {
			t.Fatalf("coalesced snapshot = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no replacement snapshot")
	}
}

// TestServerExposesOnlyProxyService verifies the local API excludes operator methods.
func TestServerExposesOnlyProxyService(t *testing.T) {
	directory, err := os.MkdirTemp("", "nstance-control-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "control.sock")
	server, err := New(path, &testHandler{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	connection, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_, err = proto.NewOperatorServiceClient(connection).RefreshConfig(ctx, &emptypb.Empty{})
	if status.Code(err).String() != "Unimplemented" {
		t.Fatalf("operator RPC error = %v", err)
	}
}

// WakeListener records the request and returns a test upstream.
func (h *testHandler) WakeListener(_ context.Context, listener string) (*proto.WakeTenantResponse, error) {
	h.listener = listener
	upstream := "10.0.0.2:6443"
	return &proto.WakeTenantResponse{
		Result:   proto.WakeTenantResponse_RESULT_WOKE,
		Status:   proto.TenantSleepStatus_TENANT_SLEEP_STATUS_AWAKE,
		Upstream: &upstream,
	}, nil
}

// TestServerExposesWakeOnProtectedUnixSocket verifies socket permissions and forwarding.
func TestServerExposesWakeOnProtectedUnixSocket(t *testing.T) {
	directory, err := os.MkdirTemp("", "nstance-wake-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "wake.sock")
	handler := &testHandler{}
	server, err := New(path, handler)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer server.Stop()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0660 {
		t.Fatalf("socket mode = %o, want 660", info.Mode().Perm())
	}
	client, err := proxy.NewUnixWaker(path)
	if err != nil {
		t.Fatalf("NewUnixWaker: %v", err)
	}
	defer func() { _ = client.Close() }()
	upstream, err := client.Wake(ctx, "api:16443")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if upstream != "10.0.0.2:6443" || handler.listener != "api:16443" {
		t.Fatalf("upstream = %q, listener = %q", upstream, handler.listener)
	}
}

// TestServerRefusesToReplaceRegularFile verifies safe socket-path handling.
func TestServerRefusesToReplaceRegularFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "wake.sock")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(path, &testHandler{})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err == nil {
		t.Fatal("Start replaced a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "keep" {
		t.Fatalf("regular file changed: content %q, error %v", content, err)
	}
}

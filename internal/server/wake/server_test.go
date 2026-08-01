// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package wake

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/proxy"
)

// testHandler records local wake requests.
type testHandler struct {
	request *proto.WakeTenantRequest
}

// WakeListener records the request and returns a test upstream.
func (h *testHandler) WakeListener(_ context.Context, request *proto.WakeTenantRequest) (*proto.WakeTenantResponse, error) {
	h.request = request
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
	server, err := New(path, -1, -1, handler)
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
	upstream, err := client.Wake(ctx, "api:16443", "red")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if upstream != "10.0.0.2:6443" || handler.request.GetListener() != "api:16443" || handler.request.GetTenant() != "red" {
		t.Fatalf("upstream = %q, request = %#v", upstream, handler.request)
	}
}

// TestServerRefusesToReplaceRegularFile verifies safe socket-path handling.
func TestServerRefusesToReplaceRegularFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "wake.sock")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(path, -1, -1, &testHandler{})
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

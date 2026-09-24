// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package wake

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/pkg/proxy"
)

// handlerState records test wake operations.
type handlerState struct {
	tenant string
	woken  string
}

// WakeAndWait records the tenant, marks it awake, and waits for readiness.
func (s *handlerState) WakeAndWait(ctx context.Context, tenant string, wait func(context.Context) error) (bool, error) {
	s.tenant = tenant
	s.woken = tenant
	return false, wait(ctx)
}

// handlerInstances stores a mutable test instance.
type handlerInstances struct {
	mu       sync.Mutex
	instance *localdb.Instance
}

// GetInstancesByGroup returns the fixture instance for the expected group.
func (s *handlerInstances) GetInstancesByGroup(tenant, group string, excludeOnDemand bool) ([]string, error) {
	if tenant != "red" || group != "control" || !excludeOnDemand {
		return nil, nil
	}
	return []string{"instance"}, nil
}

// GetInstance returns a copy of the fixture instance.
func (s *handlerInstances) GetInstance(string) (*localdb.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *s.instance
	return &copy, nil
}

// TestListenerHandlerWaitsForFreshHealthyUpstream verifies stale observations are rejected.
func TestListenerHandlerWaitsForFreshHealthyUpstream(t *testing.T) {
	now := time.Now().UTC()
	ip := "10.0.0.2"
	instances := &handlerInstances{instance: &localdb.Instance{IP4: &ip, HealthAt: timePointer(now.Add(-time.Second))}}
	state := &handlerState{}
	handler, err := NewListenerHandler(func() (proxy.Config, error) {
		return proxy.Config{Listeners: map[string]proxy.Listener{
			"api:16443": {Tenant: "red", Groups: []string{"control"}, TargetPort: 6443, ProxyPort: 16443},
		}}, nil
	}, state, instances)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	handler.pollInterval = time.Millisecond
	handler.dial = func(_ context.Context, address string) error {
		if address != "10.0.0.2:6443" {
			t.Fatalf("dial address = %q", address)
		}
		return nil
	}
	go func() {
		time.Sleep(5 * time.Millisecond)
		instances.mu.Lock()
		instances.instance.HealthAt = timePointer(now.Add(time.Second))
		instances.mu.Unlock()
	}()
	response, err := handler.WakeListener(context.Background(), "api:16443")
	if err != nil {
		t.Fatal(err)
	}
	if state.tenant != "red" || state.woken != "red" || response.GetUpstream() != "10.0.0.2:6443" {
		t.Fatalf("state = %#v, response = %#v", state, response)
	}
}

// TestListenerHandlerRejectsUnknownIdentity verifies unconfigured listeners are rejected.
func TestListenerHandlerRejectsUnknownIdentity(t *testing.T) {
	handler, err := NewListenerHandler(func() (proxy.Config, error) {
		return proxy.Config{Listeners: map[string]proxy.Listener{}}, nil
	}, &handlerState{}, &handlerInstances{instance: &localdb.Instance{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.WakeListener(context.Background(), "missing"); status.Code(err) != codes.NotFound {
		t.Fatalf("error = %v, want NotFound", err)
	}
}

// timePointer returns a pointer to value.
func timePointer(value time.Time) *time.Time { return &value }

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/localdb"
)

// sleepTestTenantState records sleep calls and exposes a fixed sleep state.
type sleepTestTenantState struct {
	sleepCalls int
	asleep     bool
}

// Sleep records a test sleep operation.
func (s *sleepTestTenantState) Sleep(ctx context.Context, _ string, _ *time.Time, check func(context.Context) error) (bool, *time.Time, error) {
	if check != nil {
		if err := check(ctx); err != nil {
			return false, nil, err
		}
	}
	s.sleepCalls++
	return false, nil, nil
}

// Wake completes a test wake operation.
func (s *sleepTestTenantState) Wake(context.Context, string) (bool, error) {
	return false, nil
}

// CreateOnDemand wakes the test tenant and runs create.
func (s *sleepTestTenantState) CreateOnDemand(ctx context.Context, _ string, create func(context.Context) error) error {
	s.asleep = false
	return create(ctx)
}

// TestSleepTenantBlockedByOnDemandInstance verifies all sleeps preserve on-demand instances.
func TestSleepTenantBlockedByOnDemandInstance(t *testing.T) {
	db, err := localdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.CreateInstance(&localdb.Instance{
		ID:        "on-demand",
		Tenant:    "red",
		Group:     "workers",
		OnDemand:  true,
		Nonce:     "nonce",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create on-demand instance: %v", err)
	}

	state := &sleepTestTenantState{}
	service := &Service{localDB: db, tenantState: state, sleepReady: true}
	ctx := context.WithValue(context.Background(), api.ClientInfoKey, &api.ClientInfo{Tenant: "red"})

	for _, ifNotBusy := range []bool{false, true} {
		response, err := service.SleepTenant(ctx, &proto.SleepTenantRequest{
			Tenant:    "red",
			IfNotBusy: ifNotBusy,
		})
		if err != nil {
			t.Fatalf("SleepTenant(if_not_busy=%v): %v", ifNotBusy, err)
		}
		if response.Result != proto.SleepTenantResponse_RESULT_BUSY || response.Status != proto.TenantSleepStatus_TENANT_SLEEP_STATUS_AWAKE {
			t.Fatalf("SleepTenant(if_not_busy=%v) = %#v, want busy and awake", ifNotBusy, response)
		}
	}
	if state.sleepCalls != 0 {
		t.Fatalf("sleep state calls = %d, want 0", state.sleepCalls)
	}
}

// TestCreateInstanceWakesSleepingTenant verifies creation wakes a sleeping tenant.
func TestCreateInstanceWakesSleepingTenant(t *testing.T) {
	state := &sleepTestTenantState{asleep: true}
	manager := &mockInstanceManager{}
	service := &Service{tenantState: state, instanceManager: manager, logger: slog.Default()}
	ctx := context.WithValue(context.Background(), api.ClientInfoKey, &api.ClientInfo{Tenant: "red"})

	_, err := service.CreateInstance(ctx, &proto.CreateInstanceRequest{
		Config: &proto.InstanceConfig{Group: "workers"},
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if state.asleep {
		t.Fatal("tenant remains asleep")
	}
	if manager.lastCreateRequest.Tenant != "red" {
		t.Fatalf("created tenant = %q, want red", manager.lastCreateRequest.Tenant)
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tenantstate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/localdb"
)

// TestCheckActivityWaitsForPostWithdrawalHealth verifies that stale idle data
// cannot satisfy the final sleep guard.
func TestCheckActivityWaitsForPostWithdrawalHealth(t *testing.T) {
	db, err := localdb.Open(filepath.Join(t.TempDir(), "instances.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	old := time.Now().UTC().Add(-time.Minute)
	if err := db.CreateInstance(&localdb.Instance{
		ID: "instance-1", Tenant: "red", Group: "web", CreatedAt: old,
		HealthAt: &old, Health: []byte(`{"metrics":{"ebpf_counters":{"443":0}}}`),
	}); err != nil {
		t.Fatal(err)
	}
	size := 1
	cfg := &config.Config{
		Groups: map[string]map[string]config.GroupConfig{
			"red": {"web": {Size: &size, LoadBalancers: []string{"public"}}},
		},
		LoadBalancers: map[string]config.LoadBalancerConfig{
			"public": {Provider: "aws", TargetGroups: []config.AWSTargetGroupConfig{{TargetPort: 443}}},
		},
	}
	cutover := &ProviderCutover{options: CutoverOptions{
		Config: func() *config.Config { return cfg }, Instances: db, PollInterval: time.Millisecond,
	}}
	withdrawalAt := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cutover.CheckActivity(ctx, "red", withdrawalAt) }()
	select {
	case err := <-done:
		t.Fatalf("stale health completed guard: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	if err := db.UpdateInstanceHealth("instance-1", []byte(`{"metrics":{"ebpf_counters":{"443":0}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	withdrawalAt = time.Now().UTC().Add(-time.Millisecond)
	if err := db.UpdateInstanceHealth("instance-1", []byte(`{"metrics":{"ebpf_counters":{"443":1}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := cutover.CheckActivity(ctx, "red", withdrawalAt); !errors.Is(err, ErrBusy) {
		t.Fatalf("active health error = %v, want %v", err, ErrBusy)
	}
}

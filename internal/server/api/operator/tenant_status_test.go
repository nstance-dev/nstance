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
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/listeneractivity"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// TestGetTenantStatusRequiresEveryListenerInstance verifies that one healthy
// instance cannot hide another instance's missing or failed activity report.
func TestGetTenantStatusRequiresEveryListenerInstance(t *testing.T) {
	db, err := localdb.Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	loader, err := config.NewLoader(config.LoaderOptions{
		Storage:      storage.NewMock(),
		CacheStorage: storage.NewMock(),
		LocalDB:      db,
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	size := 2
	loader.SetConfig(&config.Config{
		LoadBalancers: map[string]config.LoadBalancerConfig{
			"public": {Provider: "aws", TargetGroups: []config.AWSTargetGroupConfig{{TargetPort: 443, ProxyPort: 8443}}},
		},
		Groups: map[string]map[string]config.GroupConfig{
			"red": {"workers": {Size: &size, LoadBalancers: []string{"public"}}},
		},
	})
	for _, id := range []string{"instance-1", "instance-2"} {
		if err := db.CreateInstance(&localdb.Instance{ID: id, Tenant: "red", Group: "workers", Nonce: id, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	activity := listeneractivity.New()
	service := &Service{configLoader: loader, listenerActivity: activity, localDB: db, tenantState: &sleepTestTenantState{}}
	ctx := context.WithValue(context.Background(), api.ClientInfoKey, &api.ClientInfo{Tenant: "red"})
	response, err := service.GetTenantStatus(ctx, &proto.GetTenantStatusRequest{Tenant: "red"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Listeners) != 1 || response.Listeners[0].GetAvailable() {
		t.Fatalf("listeners before reports = %#v, want unavailable", response.Listeners)
	}

	old := time.Now().UTC().Add(-time.Hour)
	latest := old.Add(10 * time.Minute)
	activity.Update("red", "public:8443", "instance-1", false, true, old)
	activity.Update("red", "public:8443", "instance-2", false, true, latest)
	response, err = service.GetTenantStatus(ctx, &proto.GetTenantStatusRequest{Tenant: "red"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Listeners) != 1 || !response.Listeners[0].GetAvailable() || !response.Listeners[0].GetIdleSince().AsTime().Equal(latest) {
		t.Fatalf("listeners = %#v, want available at %s", response.Listeners, latest)
	}
	activity.Update("red", "public:8443", "instance-1", false, false, time.Now().UTC())
	response, err = service.GetTenantStatus(ctx, &proto.GetTenantStatusRequest{Tenant: "red"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Listeners[0].GetAvailable() || response.Listeners[0].GetIdleSince() != nil {
		t.Fatalf("listeners = %#v, want unavailable", response.Listeners)
	}
}

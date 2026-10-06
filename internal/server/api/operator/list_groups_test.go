// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/nstance-dev/nstance/v2/internal/server/config"
	"github.com/nstance-dev/nstance/v2/internal/server/localdb"
	"github.com/nstance-dev/nstance/v2/internal/server/storage"
)

// TestListGroupsHidesNATGroup verifies the operator cannot import or resize
// the server-derived NAT group.
func TestListGroupsHidesNATGroup(t *testing.T) {
	db, err := localdb.Open(filepath.Join(t.TempDir(), "groups.db"))
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
	size := 1
	loader.SetConfig(&config.Config{
		Templates: map[string]config.TemplateConfig{"node": {}, "nat": {}},
		Groups: map[string]map[string]config.GroupConfig{"red": {
			"workers": {Template: "node", Size: &size},
			"nat":     {Template: "nat"},
		}},
		NAT: map[string]config.NATConfig{"red": {Group: "nat"}},
	})
	for _, group := range []string{"workers", "nat"} {
		if err := db.UpsertGroup("red", group, "runtime", "infra"); err != nil {
			t.Fatal(err)
		}
	}
	groups, err := (&Service{configLoader: loader, localDB: db, logger: slog.Default()}).listGroups("red")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].GetKey() != "workers" {
		t.Fatalf("groups = %#v, want only workers", groups)
	}
}

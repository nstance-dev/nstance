// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package filegen

import (
	"context"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// TestGenerateProxyFilesUsesSharedFileSources verifies proxy files use normal file sources.
func TestGenerateProxyFilesUsesSharedFileSources(t *testing.T) {
	ctx := context.Background()
	secretStore := secrets.NewMemoryStore()
	if err := secretStore.Set(ctx, "tunnel-token", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	objectStore, err := storage.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := objectStore.Put(ctx, "shared/config", []byte("stored")); err != nil {
		t.Fatal(err)
	}
	generator := NewGenerator(nil, nil, nil, []byte("ca"), nil, secrets.NewCachedStore(secretStore, time.Minute), objectStore, nil)
	cfg := &config.Config{
		Cluster:  config.ClusterConfig{ID: "cluster"},
		Shard:    config.ShardConfig{ID: "shard", Infra: config.InfraConfig{Provider: "mock"}},
		Defaults: config.DefaultsConfig{Vars: map[string]string{"Name": "proxy"}},
		Proxy: config.ProxyRuntimeConfig{Files: map[string]config.FileConfig{
			"secret.txt": {Kind: "secret", Source: "tunnel-token"},
			"stored.txt": {Kind: "storage", Source: "shared/config"},
			"value.txt":  {Kind: "string", Template: `{{ .Vars.Name }}`},
		}},
	}
	files, err := generator.GenerateProxyFiles(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]string{
		"secret.txt": "secret",
		"stored.txt": "stored",
		"value.txt":  "proxy",
	} {
		if string(files[name]) != expected {
			t.Fatalf("%s = %q, want %q", name, files[name], expected)
		}
	}
}

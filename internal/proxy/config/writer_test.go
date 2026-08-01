// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nstance-dev/nstance/pkg/proxy"
)

// TestWriterAtomicallyReplacesReadableConfig verifies complete config replacement.
func TestWriterAtomicallyReplacesReadableConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "nstance-proxy.json")
	writer := Writer{Path: path, UID: -1, GID: -1, Mode: 0640}
	first := proxy.Config{Listeners: map[string]proxy.Listener{
		"api": {Tenant: "red", Groups: []string{"control"}, TargetPort: 6443, ProxyPort: 16443},
	}}
	if err := writer.Write(first); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Listeners["api"].Tenant != "red" {
		t.Fatalf("loaded config = %#v", loaded)
	}
	if err := writer.Write(proxy.Config{Listeners: map[string]proxy.Listener{}}); err != nil {
		t.Fatalf("replacement Write: %v", err)
	}
	loaded, err = Load(path)
	if err != nil {
		t.Fatalf("Load replacement: %v", err)
	}
	if len(loaded.Listeners) != 0 {
		t.Fatalf("replacement listeners = %#v", loaded.Listeners)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".nstance-proxy-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

// TestValidateRuntimePath verifies runtime configs cannot escape their directory.
func TestValidateRuntimePath(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory := filepath.Join(base, "run", "nstance")
	if err := ValidateRuntimePath(filepath.Join(runtimeDirectory, "proxy.json"), runtimeDirectory); err != nil {
		t.Fatalf("valid path rejected: %v", err)
	}
	for _, path := range []string{
		filepath.Join(runtimeDirectory, "subdirectory", "proxy.json"),
		filepath.Join(runtimeDirectory, "..", "proxy.json"),
	} {
		if err := ValidateRuntimePath(path, runtimeDirectory); err == nil {
			t.Fatalf("invalid path accepted: %s", path)
		}
	}

	realDirectory := filepath.Join(base, "real")
	if err := os.Mkdir(realDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(base, "runtime-link")
	if err := os.Symlink(realDirectory, symlink); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimePath(filepath.Join(symlink, "proxy.json"), symlink); err == nil {
		t.Fatal("path beneath symlinked runtime directory accepted")
	}
}

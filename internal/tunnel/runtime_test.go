// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/nstance-dev/nstance/v2/internal/server/config"
	"github.com/nstance-dev/nstance/v2/internal/server/secrets"
	"github.com/nstance-dev/nstance/v2/internal/server/storage"
)

// TestStaticPodIsHardened verifies the generated Pod's security boundary and mounts.
func TestStaticPodIsHardened(t *testing.T) {
	data, err := staticPod("api", config.TunnelPodConfig{Image: testImage(), Args: []string{"serve"}}, map[string]string{"/run/tunnel/token": "/private/001"})
	if err != nil {
		t.Fatal(err)
	}
	var pod corev1.Pod
	if err := json.Unmarshal(data, &pod); err != nil {
		t.Fatal(err)
	}
	container := pod.Spec.Containers[0]
	security := container.SecurityContext
	if !pod.Spec.HostNetwork || pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken || security == nil || security.RunAsNonRoot == nil || !*security.RunAsNonRoot || security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation || security.ReadOnlyRootFilesystem == nil || !*security.ReadOnlyRootFilesystem {
		t.Fatalf("static Pod is not hardened: %#v", pod.Spec)
	}
	if security.SeccompProfile == nil || security.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault || len(security.Capabilities.Drop) != 1 || security.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("container security context = %#v", security)
	}
	if len(container.VolumeMounts) != 1 || !container.VolumeMounts[0].ReadOnly || container.VolumeMounts[0].MountPath != "/run/tunnel/token" {
		t.Fatalf("volume mounts = %#v", container.VolumeMounts)
	}
}

// TestDedicatedTunnelPublishesAndRemovesManifest verifies static-Pod lifecycle.
func TestDedicatedTunnelPublishesAndRemovesManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	runtime := newTestRuntime(t, map[string]config.TunnelPodConfig{
		"api": {Image: testImage(), ReadinessURL: server.URL, Files: map[string]config.FileConfig{
			"/run/tunnel/secret": {Kind: "secret", Source: "credential"},
			"/run/tunnel/config": {Kind: "storage", Source: "tunnel.json"},
		}},
	})
	if err := runtime.options.Secrets.Set(context.Background(), "credential", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.options.Storage.Put(context.Background(), "tunnel.json", []byte("config")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx, "api", func() { close(ready) }) }()
	<-ready
	manifest := runtime.manifestPath("tunnel-api")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("static Pod manifest: %v", err)
	}
	for name, want := range map[string]string{"000": "config", "001": "secret"} {
		content, err := os.ReadFile(filepath.Join(runtime.options.FilesDir, "tunnel-api", name))
		if err != nil || string(content) != want {
			t.Fatalf("resolved file %s = %q, %v", name, content, err)
		}
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Run error = %v", err)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatalf("manifest remains after stop: %v", err)
	}
}

// TestNewRuntimeRemovesStaleState verifies a restarted supervisor fails closed.
func TestNewRuntimeRemovesStaleState(t *testing.T) {
	manifestDir := t.TempDir()
	filesDir := t.TempDir()
	for _, path := range []string{
		filepath.Join(manifestDir, "nstance-tunnel-api.json"),
		filepath.Join(filesDir, "tunnel-api", "000"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("stale"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	objectStorage, err := storage.NewMockStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(RuntimeOptions{
		Storage: objectStorage, Secrets: secrets.NewMemoryStore(), ManifestDir: manifestDir, FilesDir: filesDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	for _, directory := range []string{manifestDir, filesDir} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("stale entries remain in %s: %v", directory, entries)
		}
	}
}

// newTestRuntime creates a runtime with local test providers.
func newTestRuntime(t *testing.T, tunnels map[string]config.TunnelPodConfig) *Runtime {
	t.Helper()
	objectStorage, err := storage.NewMockStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(RuntimeOptions{
		Tunnels: tunnels, Storage: objectStorage, Secrets: secrets.NewMemoryStore(),
		ManifestDir: filepath.Join(t.TempDir(), "manifests"), FilesDir: filepath.Join(t.TempDir(), "files"),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.poll = time.Millisecond
	t.Cleanup(runtime.Close)
	return runtime
}

// testImage returns one immutable fixture image.
func testImage() string {
	return "example.invalid/tunnel@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}

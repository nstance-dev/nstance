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
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
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
	if security.RunAsUser != nil || security.RunAsGroup != nil {
		t.Fatalf("container must preserve the image user/group: %#v", security)
	}
	if pod.Spec.SecurityContext != nil && (pod.Spec.SecurityContext.RunAsUser != nil || pod.Spec.SecurityContext.RunAsGroup != nil || pod.Spec.SecurityContext.FSGroup != nil || len(pod.Spec.SecurityContext.SupplementalGroups) != 0) {
		t.Fatalf("Pod must not override or supplement the image identity: %#v", pod.Spec.SecurityContext)
	}
	if len(container.VolumeMounts) != 1 || !container.VolumeMounts[0].ReadOnly || container.VolumeMounts[0].MountPath != "/run/tunnel/token" {
		t.Fatalf("volume mounts = %#v", container.VolumeMounts)
	}
	data, err = staticPod("api", config.TunnelPodConfig{Image: testImage()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pod = corev1.Pod{}
	if err := json.Unmarshal(data, &pod); err != nil {
		t.Fatal(err)
	}
	if pod.Spec.SecurityContext != nil || pod.Spec.Containers[0].SecurityContext.RunAsUser != nil || pod.Spec.Containers[0].SecurityContext.RunAsGroup != nil {
		t.Fatalf("fileless tunnel must not receive a default identity/group: %#v", pod.Spec)
	}
}

// TestDedicatedTunnelPublishesAndRemovesManifest verifies static-Pod lifecycle.
func TestDedicatedTunnelPublishesAndRemovesManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	for _, test := range []struct {
		name string
		mode *os.FileMode
		want os.FileMode
	}{
		{"default", nil, 0600},
		{"private", new(os.FileMode(0600)), 0600},
		{"container-readable", new(os.FileMode(0644)), 0644},
		{"group-readable", new(os.FileMode(0640)), 0640},
		{"read-only", new(os.FileMode(0444)), 0444},
		{"no-access", new(os.FileMode(0000)), 0000},
	} {
		t.Run(test.name, func(t *testing.T) {
			objectStorage, err := storage.NewMockStorage(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := NewRuntime(RuntimeOptions{Tunnels: map[string]config.TunnelPodConfig{
				"api": {Image: testImage(), ReadinessURL: server.URL, Files: map[string]config.FileConfig{
					"/run/tunnel/secret": {Kind: "secret", Source: "credential"},
					"/run/tunnel/config": {Kind: "storage", Source: "tunnel.json"},
				}},
			}, Storage: objectStorage, Secrets: secrets.NewMemoryStore(),
				ManifestDir: t.TempDir(), FilesDir: t.TempDir(), FilesMode: test.mode,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(runtime.Close)
			if err := runtime.options.Secrets.Set(context.Background(), "credential", []byte("secret")); err != nil {
				t.Fatal(err)
			}
			if err := runtime.options.Storage.Put(context.Background(), "tunnel.json", []byte("config")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ready := make(chan struct{})
			done := make(chan error, 1)
			go func() { done <- runtime.Run(ctx, "api", func() { close(ready) }) }()
			select {
			case <-ready:
			case err := <-done:
				t.Fatalf("Run failed before readiness: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not become ready")
			}
			manifest := runtime.manifestPath("tunnel-api")
			manifestInfo, err := os.Stat(manifest)
			if err != nil {
				t.Fatalf("static Pod manifest: %v", err)
			}
			manifestOwner := manifestInfo.Sys().(*syscall.Stat_t)
			if manifestInfo.Mode().Perm() != 0600 || int(manifestOwner.Uid) != os.Geteuid() || int(manifestOwner.Gid) != os.Getegid() {
				t.Fatalf("manifest must retain process ownership and mode 0600: %v, %#v", manifestInfo.Mode(), manifestOwner)
			}
			for name, want := range map[string]string{"000": "config", "001": "secret"} {
				path := filepath.Join(runtime.options.FilesDir, "tunnel-api", name)
				if test.want&0400 != 0 {
					content, err := os.ReadFile(path)
					if err != nil || string(content) != want {
						t.Fatalf("resolved file %s = %q, %v", name, content, err)
					}
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				owner := info.Sys().(*syscall.Stat_t)
				if info.Mode().Perm() != test.want || int(owner.Uid) != os.Geteuid() || int(owner.Gid) != os.Getegid() {
					t.Fatalf("resolved file must retain process ownership and mode %04o: %v, %#v", test.want, info.Mode(), owner)
				}
			}
			if info, err := os.Stat(filepath.Join(runtime.options.FilesDir, "tunnel-api")); err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("resolved directory must remain private: %v, %v", info, err)
			}
			t.Run("mounted files", func(t *testing.T) { testMountedFiles(t, manifest, test.want) })
			cancel()
			if err := <-done; err != context.Canceled {
				t.Fatalf("Run error = %v", err)
			}
			if _, err := os.Stat(manifest); !os.IsNotExist(err) {
				t.Fatalf("manifest remains after stop: %v", err)
			}
		})
	}
}

// testMountedFiles verifies mounted reads and host-path isolation for unrelated identities.
func testMountedFiles(t *testing.T, manifestPath string, mode os.FileMode) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("read-only bind-mount access requires Linux")
	}
	for _, tool := range []string{"unshare", "mount", "setpriv"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("mounted-file test requires %s", tool)
		}
	}
	if output, err := exec.Command("unshare", "--mount", "true").CombinedOutput(); err != nil {
		t.Skipf("mount namespaces unavailable: %s", output)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var pod corev1.Pod
	if err := json.Unmarshal(data, &pod); err != nil {
		t.Fatal(err)
	}
	if pod.Spec.Containers[0].SecurityContext.RunAsUser != nil || pod.Spec.Containers[0].SecurityContext.RunAsGroup != nil {
		t.Fatal("runtime overrides the image identity")
	}
	directory, err := os.MkdirTemp("", "tunnel-mount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	for index, volume := range pod.Spec.Volumes {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			path := volume.HostPath.Path
			info, err := os.Stat(filepath.Dir(path))
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("resolved directory must remain private: %v, %v", info, err)
			}
			// Neither simulated image identity matches the writer's credentials.
			command := exec.Command("unshare", "--mount", "--fork", "sh", "-ec", `
mount --make-rprivate /
touch "$2/file"
mount --bind "$1" "$2/file"
if [ "$6" = true ]; then
setpriv --reuid="$3" --regid="$4" --clear-groups --bounding-set=-all --no-new-privs sh -c ': >> "$1"' sh "$2/file"
fi
mount -o remount,bind,ro "$2/file"
if [ "$5" = true ]; then
setpriv --reuid=10001 --regid=10002 --clear-groups --bounding-set=-all --no-new-privs cat "$2/file"
setpriv --reuid=65532 --regid=65532 --clear-groups --bounding-set=-all --no-new-privs cat "$2/file"
else
if setpriv --reuid=10001 --regid=10002 --clear-groups --bounding-set=-all --no-new-privs cat "$2/file" 2>/dev/null; then exit 13; fi
if setpriv --reuid=65532 --regid=65532 --clear-groups --bounding-set=-all --no-new-privs cat "$2/file" 2>/dev/null; then exit 14; fi
fi
if setpriv --reuid=10001 --regid=10002 --clear-groups --bounding-set=-all --no-new-privs cat "$1" 2>/dev/null; then exit 11; fi
if setpriv --reuid="$3" --regid="$4" --clear-groups --bounding-set=-all --no-new-privs sh -c 'echo changed > "$1"' sh "$2/file" 2>/dev/null; then exit 12; fi
`, "sh", path, directory, strconv.Itoa(os.Geteuid()), strconv.Itoa(os.Getegid()), strconv.FormatBool(mode&0004 != 0), strconv.FormatBool(mode&0200 != 0))
			output, err := command.CombinedOutput()
			want := []string{"configconfig", "secretsecret"}[index]
			if mode&0004 == 0 {
				want = ""
			}
			if err != nil || string(output) != want {
				t.Fatalf("mounted file access = %q, %v; want %q", output, err, want)
			}
		})
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

// testImage returns one immutable fixture image.
func testImage() string {
	return "example.invalid/tunnel@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}

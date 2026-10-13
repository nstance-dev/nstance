// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/nstance-dev/nstance/v2/internal/server/config"
	"github.com/nstance-dev/nstance/v2/internal/server/secrets"
	"github.com/nstance-dev/nstance/v2/internal/server/storage"
)

// TestStaticPodIsHardened verifies the generated Pod's security boundary and mounts.
func TestStaticPodIsHardened(t *testing.T) {
	pod := staticPod("api", "generation", config.TunnelPodConfig{Image: testImage(), Args: []string{"serve"}}, map[string]string{"/run/tunnel/token": "/private/001"})
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
	pod = staticPod("api", "generation", config.TunnelPodConfig{Image: testImage()}, nil)
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
			manifestDir := t.TempDir()
			_, endpoint := testCRI(t, manifestDir)
			runtime, err := NewRuntime(RuntimeOptions{Tunnels: map[string]config.TunnelPodConfig{
				"api": {Image: testImage(), ReadinessURL: server.URL, Files: map[string]config.FileConfig{
					"/run/tunnel/secret": {Kind: "secret", Source: "credential"},
					"/run/tunnel/config": {Kind: "storage", Source: "tunnel.json"},
				}},
			}, Storage: objectStorage, Secrets: secrets.NewMemoryStore(),
				ManifestDir: manifestDir, FilesDir: t.TempDir(), FilesMode: test.mode, RuntimeEndpoint: endpoint,
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
			go func() {
				done <- runtime.Run(ctx, "api", func(healthy bool) {
					if healthy {
						close(ready)
					}
				})
			}()
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
			pod := readTestPod(t, manifest)
			for name, want := range map[string]string{"000": "config", "001": "secret"} {
				path := filepath.Join(runtime.options.FilesDir, "tunnel-api", string(pod.UID), name)
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

// TestTunnelInlineJSONFile verifies literal JSON reaches the Pod's mounted file.
func TestTunnelInlineJSONFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	objectStorage, err := storage.NewMockStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifestDir := t.TempDir()
	_, endpoint := testCRI(t, manifestDir)
	runtime, err := NewRuntime(RuntimeOptions{
		Tunnels: map[string]config.TunnelPodConfig{"api": {
			Image: testImage(), ReadinessURL: server.URL,
			Files: map[string]config.FileConfig{"/run/tunnel/config.json": {
				Kind: "json", Template: map[string]interface{}{
					"tunnel": "existing-tunnel", "literal": "{{ .Vars.KEY }}",
					"ingress": []interface{}{map[string]interface{}{
						"service": "https://127.0.0.1:16443", "verify": true, "retries": 2,
					}},
				},
			}},
		}},
		Storage: objectStorage, Secrets: secrets.NewMemoryStore(), ManifestDir: manifestDir, FilesDir: t.TempDir(), RuntimeEndpoint: endpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx, "api", func(healthy bool) {
			if healthy {
				close(ready)
			}
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != context.Canceled {
				t.Errorf("Run error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not stop")
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not become ready")
	}
	entries, err := os.ReadDir(manifestDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one manifest: %v, %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(manifestDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var pod corev1.Pod
	if err := json.Unmarshal(data, &pod); err != nil {
		t.Fatal(err)
	}
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].HostPath == nil {
		t.Fatalf("expected a mounted JSON file: %#v", pod.Spec.Volumes)
	}
	data, err = os.ReadFile(pod.Spec.Volumes[0].HostPath.Path)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"tunnel":"existing-tunnel","literal":"{{ .Vars.KEY }}","ingress":[{"service":"https://127.0.0.1:16443","verify":true,"retries":2}]}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mounted JSON = %s, want %#v", data, want)
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

// testRuntime models kubelet's same-name handoff, with an optional paused worker.
type testRuntime struct {
	runtimeapi.UnimplementedRuntimeServiceServer
	mu     sync.Mutex
	dir    string
	paused bool
	pod    *corev1.Pod
}

// testCRI starts a local CRI server that simulates kubelet reconciliation.
func testCRI(t *testing.T, dir string) (*testRuntime, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &testRuntime{dir: dir}
	server := grpc.NewServer()
	runtimeapi.RegisterRuntimeServiceServer(server, fixture)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return fixture, listener.Addr().String()
}

// current refreshes the observed Pod unless paused; callers hold mu.
func (r *testRuntime) current() *corev1.Pod {
	if !r.paused {
		r.pod = nil
		entries, _ := os.ReadDir(r.dir)
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(r.dir, entry.Name()))
			var pod corev1.Pod
			if err == nil && json.Unmarshal(data, &pod) == nil && pod.UID != "" {
				r.pod = &pod
			}
		}
	}
	return r.pod
}

// ListPodSandbox reports the sandbox matching the observed Pod UID.
func (r *testRuntime) ListPodSandbox(_ context.Context, request *runtimeapi.ListPodSandboxRequest) (*runtimeapi.ListPodSandboxResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pod := r.current()
	response := &runtimeapi.ListPodSandboxResponse{}
	if pod != nil && request.Filter.LabelSelector["io.kubernetes.pod.uid"] == string(pod.UID) {
		response.Items = []*runtimeapi.PodSandbox{{Id: string(pod.UID), Metadata: &runtimeapi.PodSandboxMetadata{Uid: string(pod.UID)}, State: runtimeapi.PodSandboxState_SANDBOX_READY}}
	}
	return response, nil
}

// ListContainers reports the tunnel container, excluding inert replacements.
func (r *testRuntime) ListContainers(_ context.Context, request *runtimeapi.ListContainersRequest) (*runtimeapi.ListContainersResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pod := r.current()
	response := &runtimeapi.ListContainersResponse{}
	if pod != nil && request.Filter.LabelSelector["io.kubernetes.pod.uid"] == string(pod.UID) && len(pod.Spec.Containers[0].Command) == 0 {
		response.Containers = []*runtimeapi.Container{{PodSandboxId: string(pod.UID), Metadata: &runtimeapi.ContainerMetadata{Name: "tunnel"}, State: runtimeapi.ContainerState_CONTAINER_RUNNING}}
	}
	return response, nil
}

// readTestPod loads a static Pod manifest.
func readTestPod(t *testing.T, path string) corev1.Pod {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pod corev1.Pod
	if err := json.Unmarshal(data, &pod); err != nil {
		t.Fatal(err)
	}
	return pod
}

// TestTunnelRevocationBarrier covers a live old Pod and cancellation before any
// container exists. Neither an old HTTP 2xx nor empty CRI state proves handoff.
func TestTunnelRevocationBarrier(t *testing.T) {
	for _, test := range []struct {
		name               string
		started, failDrain bool
	}{
		{name: "before-start"},
		{name: "running", started: true},
		{name: "timeout", failDrain: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
			defer server.Close()
			dir := t.TempDir()
			cri, endpoint := testCRI(t, dir)
			cri.paused = true
			store, err := storage.NewMockStorage(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Put(context.Background(), "token", []byte("old")); err != nil {
				t.Fatal(err)
			}
			r, err := NewRuntime(RuntimeOptions{
				Tunnels: map[string]config.TunnelPodConfig{"api": {Image: testImage(), ReadinessURL: server.URL, Files: map[string]config.FileConfig{"/token": {Kind: "storage", Source: "token"}}}},
				Storage: store, Secrets: secrets.NewMemoryStore(), ManifestDir: dir, FilesDir: t.TempDir(), RuntimeEndpoint: endpoint,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			r.poll = 5 * time.Millisecond
			r.drainTimeout = time.Second
			if test.failDrain {
				r.drainTimeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ready, done := make(chan struct{}, 1), make(chan error, 1)
			notReady := make(chan struct{}, 1)
			go func() {
				done <- r.Run(ctx, "api", func(healthy bool) {
					if healthy {
						ready <- struct{}{}
					} else {
						notReady <- struct{}{}
					}
				})
			}()
			manifest := filepath.Join(dir, "nstance-tunnel-api.json")
			waitTestPod(t, manifest, func(pod corev1.Pod) bool { return pod.UID != "" })
			old := readTestPod(t, manifest)
			select {
			case <-ready:
				t.Fatal("unobserved generation accepted HTTP readiness")
			case <-time.After(30 * time.Millisecond):
			}
			if test.started {
				cri.mu.Lock()
				cri.paused = false
				cri.mu.Unlock()
				select {
				case <-ready:
				case <-time.After(time.Second):
					t.Fatal("Pod not ready")
				}
				cri.mu.Lock()
				cri.paused = true
				cri.mu.Unlock()
			}
			cancel()
			waitTestPod(t, manifest, func(pod corev1.Pod) bool { return pod.UID != old.UID })
			barrier := readTestPod(t, manifest)
			select {
			case <-notReady:
			default:
				t.Fatal("revoked Pod still reports readiness during handoff")
			}
			if len(barrier.Spec.Volumes) != 0 || !reflect.DeepEqual(barrier.Spec.Containers[0].Command, []string{"/"}) || barrier.Spec.Containers[0].ImagePullPolicy != corev1.PullNever {
				t.Fatalf("revocation Pod can run image code or access files: %#v", barrier.Spec)
			}
			select {
			case err := <-done:
				t.Fatalf("stop completed without kubelet handoff: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			if _, err := os.Stat(old.Spec.Volumes[0].HostPath.Path); err != nil {
				t.Fatalf("undrained files removed: %v", err)
			}
			if test.failDrain {
				select {
				case err := <-done:
					if !errors.Is(err, ErrDrain) {
						t.Fatalf("drain error = %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("drain timeout not bounded")
				}
				r.Close()
				if _, err := os.Stat(old.Spec.Volumes[0].HostPath.Path); err != nil {
					t.Fatalf("failed drain removed files: %v", err)
				}
				if readTestPod(t, manifest).UID != barrier.UID {
					t.Fatal("failed drain lost revocation intent")
				}
				return
			}
			cri.mu.Lock()
			cri.paused = false
			cri.mu.Unlock()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || errors.Is(err, ErrDrain) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("stop did not finish after handoff")
			}
			if _, err := os.Stat(manifest); !os.IsNotExist(err) {
				t.Fatalf("manifest remains: %v", err)
			}
			if err := store.Put(context.Background(), "token", []byte("new")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel = context.WithCancel(context.Background())
			defer cancel()
			go func() {
				done <- r.Run(ctx, "api", func(healthy bool) {
					if healthy {
						ready <- struct{}{}
					}
				})
			}()
			select {
			case <-ready:
			case <-time.After(time.Second):
				t.Fatal("replacement not ready")
			}
			replacement := readTestPod(t, manifest)
			if replacement.UID == old.UID || replacement.Spec.Volumes[0].HostPath.Path == old.Spec.Volumes[0].HostPath.Path {
				t.Fatal("replacement reused generation or mounted inode path")
			}
			data, err := os.ReadFile(replacement.Spec.Volumes[0].HostPath.Path)
			if err != nil || string(data) != "new" {
				t.Fatalf("replacement content = %q, %v", data, err)
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) || errors.Is(err, ErrDrain) {
				t.Fatal(err)
			}
		})
	}
}

// waitTestPod waits for a manifest that satisfies condition.
func waitTestPod(t *testing.T, path string, condition func(corev1.Pod) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		var pod corev1.Pod
		if err == nil && json.Unmarshal(data, &pod) == nil && condition(pod) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("manifest did not reach expected state")
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/nstance-dev/nstance/v2/internal/server/config"
	"github.com/nstance-dev/nstance/v2/internal/server/secrets"
	"github.com/nstance-dev/nstance/v2/internal/server/storage"
)

// DefaultRuntimeEndpoint is the local containerd CRI socket.
const DefaultRuntimeEndpoint = "unix:///run/containerd/containerd.sock"

// ErrDrain means kubelet did not confirm revocation; the supervisor must exit.
var ErrDrain = errors.New("tunnel Pod drain failed")

// RuntimeOptions contains the local paths and providers owned by the tunnel command.
type RuntimeOptions struct {
	Tunnels         map[string]config.TunnelPodConfig
	Storage         storage.Storage
	Secrets         secrets.Store
	ManifestDir     string
	FilesDir        string
	FilesMode       *os.FileMode // Nil defaults to 0600.
	RuntimeEndpoint string
}

// Runtime manages tunnel static Pods.
type Runtime struct {
	options      RuntimeOptions
	client       *http.Client
	connection   *grpc.ClientConn
	cri          runtimeapi.RuntimeServiceClient
	poll         time.Duration
	drainTimeout time.Duration
}

// NewRuntime creates a provider-neutral tunnel runtime.
func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	if options.Storage == nil || options.Secrets == nil {
		return nil, fmt.Errorf("tunnel storage and secrets providers are required")
	}
	if options.ManifestDir == "" || options.FilesDir == "" {
		return nil, fmt.Errorf("manifest and files directories are required")
	}
	if options.FilesMode == nil {
		options.FilesMode = new(os.FileMode(0600))
	}
	if options.RuntimeEndpoint == "" {
		options.RuntimeEndpoint = DefaultRuntimeEndpoint
	}
	if err := CleanRuntime(options.ManifestDir, options.FilesDir); err != nil {
		return nil, err
	}
	connection, err := grpc.NewClient(options.RuntimeEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("connect to container runtime: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	runtime := &Runtime{
		options:    options,
		connection: connection,
		cri:        runtimeapi.NewRuntimeServiceClient(connection),
		client: &http.Client{
			Transport: transport,
			Timeout:   time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		poll:         200 * time.Millisecond,
		drainTimeout: time.Minute,
	}
	return runtime, nil
}

// Close releases the runtime connection. Run owns Pod cleanup; failed drains
// retain their inert manifest and files for host-level recovery.
func (r *Runtime) Close() {
	_ = r.connection.Close()
}

// CleanRuntime removes static-Pod state that could survive a supervisor restart.
func CleanRuntime(manifestDir, filesDir string) error {
	if manifestDir == "" || filesDir == "" {
		return fmt.Errorf("manifest and files directories are required")
	}
	for _, directory := range []string{manifestDir, filesDir} {
		entries, err := os.ReadDir(directory)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read tunnel runtime directory %s: %w", directory, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			ownedManifest := directory == manifestDir && strings.HasPrefix(name, "nstance-tunnel-") && strings.HasSuffix(name, ".json")
			ownedFiles := directory == filesDir && strings.HasPrefix(name, "tunnel-")
			if ownedManifest || ownedFiles {
				if err := os.RemoveAll(filepath.Join(directory, name)); err != nil {
					return fmt.Errorf("remove stale tunnel runtime path %s: %w", filepath.Join(directory, name), err)
				}
			}
		}
	}
	return nil
}

// Run reports readiness changes and waits for kubelet to revoke the Pod on exit.
func (r *Runtime) Run(ctx context.Context, tunnelName string, ready func(bool)) (err error) {
	tunnel, ok := r.options.Tunnels[tunnelName]
	if !ok {
		return fmt.Errorf("unknown tunnel %q", tunnelName)
	}
	name := "tunnel-" + tunnelName
	uid := types.UID(rand.Text())
	filesName := filepath.Join(name, string(uid))
	files, err := r.resolveFiles(ctx, filesName, tunnel.Files)
	if err != nil {
		return err
	}
	// Publication may succeed even if syncing the directory fails. Always fence
	// this generation before returning, including canceled or failed startup.
	defer func() {
		ready(false)
		if drainErr := r.drain(name, uid, tunnel.Image); drainErr != nil {
			err = errors.Join(err, fmt.Errorf("%w: %s: %w", ErrDrain, tunnelName, drainErr))
		}
	}()
	if err := r.publishPod(staticPod(name, uid, tunnel, files)); err != nil {
		return err
	}
	timeout := tunnel.ReadinessTimeout.Duration()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if err := r.waitReady(ctx, uid, tunnel.ReadinessURL, timeout); err != nil {
		return err
	}
	ready(true)
	return r.monitorReady(ctx, uid, tunnel.ReadinessURL)
}

// drain positively confirms kubelet has replaced the revoked generation. Merely
// finding no live containers is insufficient: an in-flight SyncPod can recreate
// them. A same-name, fresh-UID sandbox cannot start until the old worker retires.
func (r *Runtime) drain(name string, uid types.UID, image string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.drainTimeout)
	defer cancel()
	barrierUID := types.UID(rand.Text())
	barrier := staticPod(name, barrierUID, config.TunnelPodConfig{Image: image}, nil)
	// Never run the image's entrypoint or mount tunnel files. Executing a directory
	// always fails; only the sandbox is needed, without another image or CNI.
	barrier.Spec.Containers[0].Command = []string{"/"}
	barrier.Spec.Containers[0].ImagePullPolicy = corev1.PullNever
	barrier.Spec.TerminationGracePeriodSeconds = new(int64)
	if err := r.publishPod(barrier); err != nil {
		return err
	}
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		barrierReady, _, _, err := r.podState(ctx, barrierUID)
		if err != nil {
			return err
		}
		if barrierReady {
			_, _, stopped, err := r.podState(ctx, uid)
			if err != nil {
				return err
			}
			if stopped {
				if err := os.Remove(r.manifestPath(name)); err != nil {
					return err
				}
				return os.RemoveAll(filepath.Join(r.options.FilesDir, name))
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for kubelet revocation: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// podState observes only this generation, never a previous run or another Pod.
func (r *Runtime) podState(ctx context.Context, uid types.UID) (ready, running, stopped bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	labels := map[string]string{"io.kubernetes.pod.uid": string(uid)}
	sandboxes, err := r.cri.ListPodSandbox(ctx, &runtimeapi.ListPodSandboxRequest{Filter: &runtimeapi.PodSandboxFilter{LabelSelector: labels}})
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return false, false, false, fmt.Errorf("observe tunnel sandbox: %w", err)
	}
	containers, err := r.cri.ListContainers(ctx, &runtimeapi.ListContainersRequest{Filter: &runtimeapi.ContainerFilter{LabelSelector: labels}})
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return false, false, false, fmt.Errorf("observe tunnel containers: %w", err)
	}
	stopped = true
	for _, sandbox := range sandboxes.Items {
		if sandbox.State != runtimeapi.PodSandboxState_SANDBOX_READY {
			continue
		}
		ready, stopped = true, false
		for _, container := range containers.Containers {
			if container.PodSandboxId == sandbox.Id && container.Metadata.Name == "tunnel" && container.State == runtimeapi.ContainerState_CONTAINER_RUNNING {
				running = true
			}
		}
	}
	for _, container := range containers.Containers {
		if container.State != runtimeapi.ContainerState_CONTAINER_EXITED {
			stopped = false
		}
	}
	return ready, running, stopped, nil
}

// publishPod atomically writes a private static Pod manifest for kubelet.
func (r *Runtime) publishPod(pod corev1.Pod) error {
	data, err := json.MarshalIndent(pod, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(r.options.ManifestDir, pod.Name+".json", data, 0600)
}

// resolveFiles materializes one runtime's files and returns destination-to-host paths.
func (r *Runtime) resolveFiles(ctx context.Context, runtimeName string, files map[string]config.FileConfig) (map[string]string, error) {
	result := make(map[string]string, len(files))
	destinations := make([]string, 0, len(files))
	for destination := range files {
		destinations = append(destinations, destination)
	}
	sort.Strings(destinations)
	for index, destination := range destinations {
		file := files[destination]
		var content []byte
		var err error
		switch file.Kind {
		case "secret":
			content, err = r.options.Secrets.Get(ctx, file.Source)
		case "storage":
			content, _, err = r.options.Storage.Get(ctx, file.Source)
		case "json":
			content, err = json.MarshalIndent(file.Template, "", "  ")
		default:
			err = fmt.Errorf("unsupported file kind %q", file.Kind)
		}
		if err != nil {
			_ = os.RemoveAll(filepath.Join(r.options.FilesDir, runtimeName))
			return nil, fmt.Errorf("resolve %s file %s: %w", runtimeName, destination, err)
		}
		directory := filepath.Join(r.options.FilesDir, runtimeName)
		filename := fmt.Sprintf("%03d", index)
		if err := writeAtomic(directory, filename, content, *r.options.FilesMode); err != nil {
			_ = os.RemoveAll(directory)
			return nil, err
		}
		result[destination] = filepath.Join(directory, filename)
	}
	return result, nil
}

// waitReady requires this generation's running container before probing HTTP.
func (r *Runtime) waitReady(ctx context.Context, uid types.UID, rawURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		podReady, running, _, err := r.podState(ctx, uid)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if podReady && running && r.isReady(ctx, rawURL) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("readiness timeout: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// monitorReady reports a running tunnel that loses readiness.
func (r *Runtime) monitorReady(ctx context.Context, uid types.UID, rawURL string) error {
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			podReady, running, _, err := r.podState(ctx, uid)
			if err != nil && ctx.Err() == nil {
				return err
			}
			if !podReady || !running || !r.isReady(ctx, rawURL) {
				if err := ctx.Err(); err != nil {
					return err
				}
				return fmt.Errorf("tunnel lost readiness")
			}
		}
	}
}

// isReady reports whether one loopback readiness probe succeeds.
func (r *Runtime) isReady(ctx context.Context, rawURL string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	response, err := r.client.Do(request)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 300
}

// manifestPath returns one supervisor-owned static-Pod path.
func (r *Runtime) manifestPath(name string) string {
	return filepath.Join(r.options.ManifestDir, "nstance-"+name+".json")
}

// staticPod renders one fixed, hardened kubelet static Pod.
func staticPod(name string, uid types.UID, pod config.TunnelPodConfig, files map[string]string) corev1.Pod {
	trueValue := true
	falseValue := false
	container := corev1.Container{
		Name: "tunnel", Image: pod.Image, Args: append([]string(nil), pod.Args...), ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &falseValue, ReadOnlyRootFilesystem: &trueValue, RunAsNonRoot: &trueValue,
			Capabilities:   &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
	volumes := make([]corev1.Volume, 0, len(files))
	destinations := make([]string, 0, len(files))
	for destination := range files {
		destinations = append(destinations, destination)
	}
	sort.Strings(destinations)
	fileType := corev1.HostPathFile
	for index, destination := range destinations {
		volumeName := fmt.Sprintf("file-%d", index)
		hostPath := files[destination]
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: volumeName, MountPath: destination, ReadOnly: true})
		volumes = append(volumes, corev1.Volume{Name: volumeName, VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: hostPath, Type: &fileType}}})
	}
	return corev1.Pod{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{Name: "nstance-" + name, Namespace: "kube-system", UID: uid, Labels: map[string]string{"app.kubernetes.io/managed-by": "nstance-server-tunnel"}},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: &falseValue, EnableServiceLinks: &falseValue,
			HostNetwork: true, DNSPolicy: corev1.DNSClusterFirstWithHostNet, RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{container}, Volumes: volumes,
		},
	}
}

// writeAtomic securely replaces one supervisor-owned file.
func writeAtomic(directory, name string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create directory %s: %w", directory, err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse non-directory path %s", directory)
	}
	temporary, err := os.CreateTemp(directory, ".nstance-tunnel-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, name)); err != nil {
		return fmt.Errorf("publish file: %w", err)
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = directoryHandle.Close() }()
	return directoryHandle.Sync()
}

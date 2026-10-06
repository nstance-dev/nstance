// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/nstance-dev/nstance/v2/internal/server/config"
	"github.com/nstance-dev/nstance/v2/internal/server/secrets"
	"github.com/nstance-dev/nstance/v2/internal/server/storage"
)

// RuntimeOptions contains the local paths and providers owned by the tunnel command.
type RuntimeOptions struct {
	Tunnels     map[string]config.TunnelPodConfig
	Storage     storage.Storage
	Secrets     secrets.Store
	ManifestDir string
	FilesDir    string
}

// Runtime manages tunnel static Pods.
type Runtime struct {
	options RuntimeOptions
	client  *http.Client
	poll    time.Duration
}

// NewRuntime creates a provider-neutral tunnel runtime.
func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	if options.Storage == nil || options.Secrets == nil {
		return nil, fmt.Errorf("tunnel storage and secrets providers are required")
	}
	if options.ManifestDir == "" || options.FilesDir == "" {
		return nil, fmt.Errorf("manifest and files directories are required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	runtime := &Runtime{
		options: options,
		client: &http.Client{
			Transport: transport,
			Timeout:   time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		poll: 200 * time.Millisecond,
	}
	if err := CleanRuntime(options.ManifestDir, options.FilesDir); err != nil {
		return nil, err
	}
	return runtime, nil
}

// Close removes supervisor-owned manifests and resolved files.
func (r *Runtime) Close() {
	_ = CleanRuntime(r.options.ManifestDir, r.options.FilesDir)
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

// Run realizes one logical tunnel until ctx is canceled.
func (r *Runtime) Run(ctx context.Context, tunnelName string, ready func()) error {
	tunnel, ok := r.options.Tunnels[tunnelName]
	if !ok {
		return fmt.Errorf("unknown tunnel %q", tunnelName)
	}
	if err := r.startPod(ctx, "tunnel-"+tunnelName, tunnel); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(r.manifestPath("tunnel-" + tunnelName))
		_ = os.RemoveAll(filepath.Join(r.options.FilesDir, "tunnel-"+tunnelName))
	}()
	ready()
	return r.monitorReady(ctx, tunnel.ReadinessURL)
}

// startPod resolves files, publishes a hardened static Pod, and waits for readiness.
func (r *Runtime) startPod(ctx context.Context, name string, pod config.TunnelPodConfig) error {
	files, err := r.resolveFiles(ctx, name, pod.Files)
	if err != nil {
		return err
	}
	manifest, err := staticPod(name, pod, files)
	if err != nil {
		_ = os.RemoveAll(filepath.Join(r.options.FilesDir, name))
		return err
	}
	if err := writeAtomic(r.options.ManifestDir, filepath.Base(r.manifestPath(name)), manifest, 0600); err != nil {
		_ = os.RemoveAll(filepath.Join(r.options.FilesDir, name))
		return err
	}
	timeout := pod.ReadinessTimeout.Duration()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if err := r.waitReady(ctx, pod.ReadinessURL, timeout); err != nil {
		_ = os.Remove(r.manifestPath(name))
		_ = os.RemoveAll(filepath.Join(r.options.FilesDir, name))
		return err
	}
	return nil
}

// resolveFiles fetches one runtime's files and returns destination-to-host paths.
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
		}
		if err != nil {
			_ = os.RemoveAll(filepath.Join(r.options.FilesDir, runtimeName))
			return nil, fmt.Errorf("resolve %s file %s: %w", runtimeName, destination, err)
		}
		directory := filepath.Join(r.options.FilesDir, runtimeName)
		filename := fmt.Sprintf("%03d", index)
		if err := writeAtomic(directory, filename, content, 0600); err != nil {
			_ = os.RemoveAll(directory)
			return nil, err
		}
		result[destination] = filepath.Join(directory, filename)
	}
	return result, nil
}

// waitReady polls a fixed loopback endpoint until success or timeout.
func (r *Runtime) waitReady(ctx context.Context, rawURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		if r.isReady(ctx, rawURL) {
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
func (r *Runtime) monitorReady(ctx context.Context, rawURL string) error {
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if !r.isReady(ctx, rawURL) {
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
func staticPod(name string, pod config.TunnelPodConfig, files map[string]string) ([]byte, error) {
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
	manifest := corev1.Pod{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{Name: "nstance-" + name, Namespace: "kube-system", Labels: map[string]string{"app.kubernetes.io/managed-by": "nstance-server-tunnel"}},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: &falseValue, EnableServiceLinks: &falseValue,
			HostNetwork: true, DNSPolicy: corev1.DNSClusterFirstWithHostNet, RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{container}, Volumes: volumes,
		},
	}
	return json.MarshalIndent(manifest, "", "  ")
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

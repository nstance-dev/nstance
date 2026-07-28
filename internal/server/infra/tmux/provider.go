// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tmux

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	provider "github.com/nstance-dev/nstance/internal/server/infra/provider"
)

const (
	devPanePrefix        = "nstance-agent-"
	devTempDirPrefix     = "nstance-dev-"
	devTmuxSessionPrefix = "nstance-"
	devTmuxSessionSuffix = "-agents"
)

// Provider implements the Provider interface for local development
// using tmux to manage nstance-agent processes
type Provider struct {
	config           provider.ProviderConfig
	logger           *slog.Logger
	tmuxSession      string // tmux session name for spawned agents
	skipBinaryCheck  bool
	devK8sDir        string // Directory where dev-k8s stores resources (for fake Node resource creation)
	registrationAddr string // Server registration address for agents to connect to
	agentAddr        string // Server agent RPC address for agents to connect to
}

// Options contains configuration for the dev provider
type Options struct {
	Config           provider.ProviderConfig
	Logger           *slog.Logger
	Shard            string // Shard ID (used to derive tmux session name)
	SkipBinaryCheck  bool
	DevK8sDir        string // Directory where dev-k8s stores resources
	RegistrationAddr string // Server registration address for agents to connect to
	AgentAddr        string // Server agent RPC address for agents to connect to
}

// NewProvider creates a new development provider
func NewProvider(opts Options) *Provider {
	// Derive tmux session name from shard: nstance-{shard}-agents
	shard := "dev"
	if opts.Shard != "" {
		shard = opts.Shard
	}
	tmuxSession := devTmuxSessionPrefix + shard + devTmuxSessionSuffix

	p := &Provider{
		config:           opts.Config,
		logger:           opts.Logger,
		tmuxSession:      tmuxSession,
		skipBinaryCheck:  opts.SkipBinaryCheck,
		devK8sDir:        opts.DevK8sDir,
		registrationAddr: opts.RegistrationAddr,
		agentAddr:        opts.AgentAddr,
	}

	// Ensure tmux session exists on provider creation
	// This handles the case where the server restarts and the session was killed
	if err := p.ensureTmuxSession(context.Background()); err != nil {
		opts.Logger.Warn("Failed to ensure tmux session on startup", "error", err)
	}

	return p
}

func (p *Provider) Kind() string {
	return "tmux"
}

// CreateInstance creates a new tmux pane running a new nstance-agent process
func (p *Provider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.CreateInstanceResponse, error) {
	p.logger.Info("Creating dev instance", "instance_id", req.InstanceID)

	// Ensure nstance-agent binary is built
	agentBinaryPath, err := p.ensureAgentBinary()
	if err != nil {
		return nil, fmt.Errorf("ensuring agent binary: %w", err)
	}

	// Create temporary identity directories
	tempDir, err := os.MkdirTemp("", devTempDirPrefix+req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("creating temp directory: %w", err)
	}
	identityDir := filepath.Join(tempDir, "identity")
	if err := os.MkdirAll(identityDir, 0700); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("creating temp identity directory: %w", err)
	}
	keysDir := filepath.Join(tempDir, "keys")
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("creating temp keys directory: %w", err)
	}
	recvDir := filepath.Join(tempDir, "recv")
	if err := os.MkdirAll(recvDir, 0700); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("creating temp recv directory: %w", err)
	}

	// Write JWT nonce file
	jwtPath := filepath.Join(identityDir, "nonce.jwt")
	if err := os.WriteFile(jwtPath, []byte(req.Nonce), 0600); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("writing JWT file: %w", err)
	}

	// Write CA certificate file
	caCertPath := filepath.Join(identityDir, "ca.crt")
	if err := os.WriteFile(caCertPath, req.CACertPEM, 0600); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("writing CA cert file: %w", err)
	}

	// Create new tmux pane with agent
	paneName := devPanePrefix + req.InstanceID

	agentCmd := fmt.Sprintf(
		`env NSTANCE_SERVER_REGISTRATION_ADDR=%s NSTANCE_SERVER_AGENT_ADDR=%s NSTANCE_INSTANCE_ID=%s NSTANCE_INSTANCE_HOSTNAME=%s NSTANCE_IDENTITY_DIR=%s NSTANCE_KEYS_DIR=%s NSTANCE_RECV_DIR=%s NSTANCE_REPORT_INTERVAL=1s %s`,
		p.registrationAddr, p.agentAddr, req.InstanceID, req.InstanceID, identityDir, keysDir, recvDir, agentBinaryPath,
	)
	tmuxCmd := exec.CommandContext(ctx, "tmux", "new-window", "-t", p.tmuxSession, "-n", paneName, agentCmd)
	tmuxCmd.Env = p.getCleanTmuxEnv()
	if err := tmuxCmd.Run(); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("creating tmux pane: %w", err)
	}
	p.logger.Info("Created dev instance", "instance_id", req.InstanceID, "temp_dir", tempDir)

	// Create fake Kubernetes Node for dev-k8s
	if err := p.createFakeNode(req.InstanceID, paneName); err != nil {
		p.logger.Warn("Failed to create fake Node", "instance_id", req.InstanceID, "error", err)
	}

	return &provider.CreateInstanceResponse{
		InstanceID:         req.InstanceID,
		ProviderInstanceID: paneName,
		Status:             provider.StatusRunning,
		PrivateIPv4:        "127.0.0.1",
		PrivateIPv6:        "::1",
		Hostname:           req.InstanceID,
		LaunchedAt:         time.Now().UTC(),
		Tags:               req.CustomTags,
	}, nil
}

// DeleteInstance terminates an agent instance by provider instance ID (pane name)
func (p *Provider) DeleteInstance(ctx context.Context, _, paneName string) error {
	instanceID := strings.TrimPrefix(paneName, devPanePrefix)
	p.logger.Info("Deleting dev instance", "pane_name", paneName, "instance_id", instanceID)

	// Find and kill the tmux pane
	tmuxCmd := exec.CommandContext(ctx, "tmux", "kill-window", "-t", p.tmuxSession+":"+paneName)
	tmuxCmd.Env = p.getCleanTmuxEnv()
	if err := tmuxCmd.Run(); err != nil {
		p.logger.Warn("Failed to kill tmux pane", "pane_name", paneName, "error", err)
	}

	// Clean up temporary directory
	if err := p.cleanupTempDir(instanceID); err != nil {
		p.logger.Warn("Failed to cleanup temp directory", "instance_id", instanceID, "error", err)
	}

	// Delete fake Kubernetes Node from dev-k8s
	if err := p.deleteFakeNode(instanceID); err != nil {
		p.logger.Warn("Failed to delete fake Node", "instance_id", instanceID, "error", err)
	}

	p.logger.Info("Deleted dev instance", "pane_name", paneName, "instance_id", instanceID)
	return nil
}

// GetInstanceStatus returns the current status of an instance
func (p *Provider) GetInstanceStatus(ctx context.Context, _, paneName string) (*provider.InstanceStatus, error) {
	instanceID := strings.TrimPrefix(paneName, devPanePrefix)

	// Check if window exists and if its pane process is alive
	// Format: window_name:pane_dead (pane_dead is 1 if the process has exited)
	tmuxCmd := exec.CommandContext(ctx, "tmux", "list-windows", "-t", p.tmuxSession, "-F", "#{window_name}:#{pane_dead}")
	tmuxCmd.Env = p.getCleanTmuxEnv()
	output, err := tmuxCmd.Output()
	if err != nil {
		return &provider.InstanceStatus{
			InstanceID:         instanceID,
			ProviderInstanceID: paneName,
			Status:             provider.StatusDeleted,
		}, nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		windowName := parts[0]
		paneDead := parts[1]

		if windowName == paneName {
			// If pane_dead is "1", the process has exited
			if paneDead == "1" {
				return &provider.InstanceStatus{
					InstanceID:         instanceID,
					ProviderInstanceID: paneName,
					Status:             provider.StatusDeleted,
				}, nil
			}
			return &provider.InstanceStatus{
				InstanceID:         instanceID,
				ProviderInstanceID: paneName,
				Status:             provider.StatusRunning,
				InstanceType:       "dev",
				PrivateIPv4:        "127.0.0.1",
				PrivateIPv6:        "::1",
				Hostname:           instanceID,
				LaunchedAt:         time.Now().UTC(), // We don't track actual launch time for dev
				Tags:               make(map[string]string),
				Region:             "local",
				Zone:               "dev",
			}, nil
		}
	}

	return &provider.InstanceStatus{
		InstanceID:         instanceID,
		ProviderInstanceID: paneName,
		Status:             provider.StatusDeleted,
	}, nil
}

// ListInstances returns instances with pagination support
func (p *Provider) ListInstances(ctx context.Context, req provider.ListInstancesRequest) (*provider.ListInstancesResponse, error) {
	var instances []*provider.InstanceStatus

	// Check if session exists
	if p.sessionExists(ctx) {
		// List all windows in our session with pane status
		// Format: window_name:pane_dead
		tmuxCmd := exec.CommandContext(ctx, "tmux", "list-windows", "-t", p.tmuxSession, "-F", "#{window_name}:#{pane_dead}")
		tmuxCmd.Env = p.getCleanTmuxEnv()
		output, err := tmuxCmd.Output()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			for _, line := range lines {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) != 2 {
					continue
				}
				windowName := parts[0]
				paneDead := parts[1]

				if strings.HasPrefix(windowName, devPanePrefix) {
					instanceID := strings.TrimPrefix(windowName, devPanePrefix)
					status := provider.StatusRunning
					if paneDead == "1" {
						status = provider.StatusDeleted
					}
					instances = append(instances, &provider.InstanceStatus{
						InstanceID:         instanceID,
						ProviderInstanceID: windowName,
						Status:             status,
						InstanceType:       "dev",
						PrivateIPv4:        "127.0.0.1",
						PrivateIPv6:        "::1",
						Hostname:           instanceID,
						LaunchedAt:         time.Now().UTC(),
						Tags:               make(map[string]string),
						Region:             "local",
						Zone:               "dev",
					})
				}
			}
		}
	}

	// Simple pagination for dev provider
	total := len(instances)
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}

	offset := 0
	if req.NextToken != "" {
		if parsed, err := strconv.Atoi(req.NextToken); err == nil {
			offset = parsed
		}
	}

	end := offset + limit
	if end > total {
		end = total
	}

	if offset >= total {
		return &provider.ListInstancesResponse{
			Instances: []*provider.InstanceStatus{},
			NextToken: "",
			Total:     total,
		}, nil
	}

	paginatedInstances := instances[offset:end]
	nextToken := ""
	if end < total {
		nextToken = strconv.Itoa(end)
	}

	return &provider.ListInstancesResponse{
		Instances: paginatedInstances,
		NextToken: nextToken,
		Total:     total,
	}, nil
}

// ensureTmuxSession creates the tmux session if it doesn't exist
func (p *Provider) ensureTmuxSession(ctx context.Context) error {
	if p.sessionExists(ctx) {
		return nil
	}

	p.logger.Info("Creating tmux session", "session", p.tmuxSession)
	tmuxCmd := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", p.tmuxSession)
	tmuxCmd.Env = p.getCleanTmuxEnv()
	if err := tmuxCmd.Run(); err != nil {
		return fmt.Errorf("creating tmux session: %w", err)
	}

	return nil
}

// ensureAgentBinary ensures that the nstance-agent binary is built
func (p *Provider) ensureAgentBinary() (string, error) {
	if p.skipBinaryCheck {
		return "sleep 3600", nil // Use a long-running command for tests
	}

	// Use Air for hot-reload during development
	return "air -c scripts/air/agent.toml", nil
}

// sessionExists checks if the tmux session exists
func (p *Provider) sessionExists(ctx context.Context) bool {
	tmuxCmd := exec.CommandContext(ctx, "tmux", "has-session", "-t", p.tmuxSession)
	tmuxCmd.Env = p.getCleanTmuxEnv()
	return tmuxCmd.Run() == nil
}

// getCleanTmuxEnv returns the current environment with TMUX variable filtered out
// to ensure we use the default/user tmux server instead of nesting inside Overmind's tmux session
func (p *Provider) getCleanTmuxEnv() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "TMUX=") {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// cleanupTempDir removes temporary directory for an instance
func (p *Provider) cleanupTempDir(instanceID string) error {
	pattern := filepath.Join(os.TempDir(), devTempDirPrefix+instanceID+"*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	for _, match := range matches {
		if err := os.RemoveAll(match); err != nil {
			return err
		}
	}

	return nil
}

// AssignLeaderNetwork is a no-op for dev provider
func (p *Provider) AssignLeaderNetwork(ctx context.Context, providerInstanceID string, ln provider.LeaderNetwork) error {
	p.logger.Info("Dev leader network assign operation (no-op)", "instance_id", providerInstanceID, "ip", ln.IP)
	return nil
}

// ReleaseLeaderNetwork is a no-op for dev provider
func (p *Provider) ReleaseLeaderNetwork(ctx context.Context, providerInstanceID string, ln provider.LeaderNetwork) error {
	p.logger.Info("Dev leader network release operation (no-op)", "instance_id", providerInstanceID, "ip", ln.IP)
	return nil
}

// CheckSubnetCapacity simulates checking subnet capacity (always returns true for dev)
func (p *Provider) CheckSubnetCapacity(ctx context.Context, subnetID string) (bool, error) {
	p.logger.Debug("Dev: Checking subnet capacity (always true)", "subnet_id", subnetID)
	return true, nil
}

// RegisterWithLB is a no-op for dev provider
func (p *Provider) RegisterWithLB(ctx context.Context, req provider.RegisterLBRequest) error {
	p.logger.Info("Dev LoadBalancer register operation (no-op)", "instance_id", req.ProviderInstanceID)
	return nil
}

// DeregisterFromLB is a no-op for dev provider
func (p *Provider) DeregisterFromLB(ctx context.Context, req provider.DeregisterLBRequest) error {
	p.logger.Info("Dev LoadBalancer deregister operation (no-op)", "instance_id", req.ProviderInstanceID)
	return nil
}

// ListLBInstances returns an empty list for dev provider
func (p *Provider) ListLBInstances(ctx context.Context, req provider.ListLBInstancesRequest) ([]string, error) {
	p.logger.Debug("Dev LoadBalancer list operation (returns empty)", "req", req)
	return []string{}, nil
}

// createFakeNode creates a fake Kubernetes Node JSON file in dev-k8s directory
func (p *Provider) createFakeNode(instanceID, providerInstanceID string) error {
	if p.devK8sDir == "" {
		return nil // dev-k8s integration not configured
	}

	nodesDir := filepath.Join(p.devK8sDir, "nodes")
	if err := os.MkdirAll(nodesDir, 0755); err != nil {
		return fmt.Errorf("creating nodes directory: %w", err)
	}

	node := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]interface{}{
			"name":              instanceID,
			"uid":               fmt.Sprintf("dev-node-%s-%d", instanceID, time.Now().UTC().UnixNano()),
			"creationTimestamp": time.Now().UTC().Format(time.RFC3339),
			"resourceVersion":   "1",
			"labels": map[string]string{
				"kubernetes.io/hostname":           instanceID,
				"node.kubernetes.io/instance-type": "dev",
			},
		},
		"spec": map[string]interface{}{
			"providerID": fmt.Sprintf("dev:///%s", providerInstanceID),
		},
		"status": map[string]interface{}{
			"conditions": []map[string]interface{}{
				{
					"type":   "Ready",
					"status": "True",
					"reason": "KubeletReady",
				},
			},
			"addresses": []map[string]interface{}{
				{"type": "InternalIP", "address": "127.0.0.1"},
				{"type": "Hostname", "address": instanceID},
			},
		},
	}

	nodePath := filepath.Join(nodesDir, instanceID+".json")
	data, err := jsonMarshalIndent(node)
	if err != nil {
		return fmt.Errorf("marshaling node: %w", err)
	}

	if err := os.WriteFile(nodePath, data, 0644); err != nil {
		return fmt.Errorf("writing node file: %w", err)
	}

	p.logger.Info("Created fake Node", "instance_id", instanceID, "path", nodePath)
	return nil
}

// deleteFakeNode removes the fake Kubernetes Node JSON file from dev-k8s directory
func (p *Provider) deleteFakeNode(instanceID string) error {
	if p.devK8sDir == "" {
		return nil // dev-k8s integration not configured
	}

	nodePath := filepath.Join(p.devK8sDir, "nodes", instanceID+".json")
	if err := os.Remove(nodePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing node file: %w", err)
	}

	p.logger.Info("Deleted fake Node", "instance_id", instanceID)
	return nil
}

// jsonMarshalIndent is a helper to marshal JSON with indentation
func jsonMarshalIndent(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tenantstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/nstance-dev/nstance/v2/internal/proto"
	"github.com/nstance-dev/nstance/v2/internal/server/config"
	"github.com/nstance-dev/nstance/v2/internal/server/infra"
	"github.com/nstance-dev/nstance/v2/internal/server/localdb"
)

// ErrBusy indicates that fresh eBPF observations found active connections.
var ErrBusy = errors.New("tenant has active connections")

// TunnelController controls the leader-local wake tunnel.
type TunnelController interface {
	SetDesired(string, proto.TunnelDesiredState_State) (uint64, error)
	Wait(context.Context, string, uint64) (*proto.TunnelStatus, error)
}

// CutoverOptions contains dependencies for provider and tunnel cutovers.
type CutoverOptions struct {
	Config           func() *config.Config
	Instances        *localdb.DB
	Provider         infra.Provider
	Targets          TargetManager
	ServerInstanceID string
	Tunnel           func() TunnelController
	PollInterval     time.Duration
	FreshFor         time.Duration
}

// TargetManager serializes cutover target mutations with ordinary instance
// load-balancer reconciliation.
type TargetManager interface {
	RegisterTarget(context.Context, infra.RegisterLBRequest) error
	DeregisterTarget(context.Context, infra.DeregisterLBRequest) error
}

// ProviderCutover implements safe provider and wake-tunnel ordering.
type ProviderCutover struct {
	options CutoverOptions
}

// namedLoadBalancer associates a configuration key with its load balancer.
type namedLoadBalancer struct {
	name   string
	config config.LoadBalancerConfig
}

// NewProviderCutover creates a concrete tenant cutover implementation.
func NewProviderCutover(options CutoverOptions) (*ProviderCutover, error) {
	if options.Config == nil || options.Instances == nil || options.Provider == nil || options.Targets == nil || options.ServerInstanceID == "" {
		return nil, fmt.Errorf("config, instances, provider, targets, and server instance ID are required")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 200 * time.Millisecond
	}
	if options.FreshFor <= 0 {
		options.FreshFor = 2 * time.Minute
	}
	return &ProviderCutover{options: options}, nil
}

// InstallWakePath enables provider prerequisites and installs every wake path.
func (c *ProviderCutover) InstallWakePath(ctx context.Context, tenant string) error {
	for _, item := range c.loadBalancers(tenant) {
		name, lb := item.name, item.config
		if lb.Provider == "tunnel" {
			controller := c.tunnel()
			if controller == nil {
				return fmt.Errorf("wake tunnel %s is unavailable", name)
			}
			if _, err := controller.SetDesired(name, proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING); err != nil {
				return fmt.Errorf("start wake tunnel %s: %w", name, err)
			}
			continue
		}
		providerConfig := infra.LoadBalancerConfigForProvider(lb)
		if err := c.options.Targets.RegisterTarget(ctx, infra.RegisterLBRequest{ProviderInstanceID: c.options.ServerInstanceID, LBConfig: providerConfig, Zone: c.options.Config().Shard.Infra.Zone, WakeProxy: true}); err != nil {
			return fmt.Errorf("register wake proxy with %s: %w", name, err)
		}
	}
	return nil
}

// WaitWakePathReady waits for explicit provider health or tunnel readiness.
func (c *ProviderCutover) WaitWakePathReady(ctx context.Context, tenant string) error {
	for _, item := range c.loadBalancers(tenant) {
		name, lb := item.name, item.config
		if lb.Provider == "tunnel" {
			controller := c.tunnel()
			if controller == nil {
				return fmt.Errorf("wake tunnel %s is unavailable", name)
			}
			revision, err := controller.SetDesired(name, proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_RUNNING)
			if err != nil {
				return err
			}
			status, err := controller.Wait(ctx, name, revision)
			if err != nil {
				return err
			}
			if status.GetState() != proto.TunnelStatus_TUNNEL_STATUS_STATE_READY {
				return fmt.Errorf("wake tunnel %s failed: %s", name, status.GetError())
			}
			continue
		}
		req := infra.RegisterLBRequest{ProviderInstanceID: c.options.ServerInstanceID, LBConfig: infra.LoadBalancerConfigForProvider(lb), Zone: c.options.Config().Shard.Infra.Zone, WakeProxy: true}
		if err := c.waitState(ctx, req, infra.LBTargetHealthy); err != nil {
			return fmt.Errorf("wake proxy %s: %w", name, err)
		}
	}
	return nil
}

// BeginTargetWithdrawal starts provider draining after the wake path is healthy.
func (c *ProviderCutover) BeginTargetWithdrawal(ctx context.Context, tenant string) error {
	targets, err := c.instanceTargets(tenant)
	if err != nil {
		return err
	}
	providerLoadBalancers := 0
	for _, item := range c.loadBalancers(tenant) {
		lb := item.config
		if lb.Provider != "tunnel" {
			providerLoadBalancers++
		}
	}
	if providerLoadBalancers > 0 && len(targets) == 0 {
		return fmt.Errorf("refusing to withdraw instance targets without an observed target")
	}
	for _, target := range targets {
		if err := c.options.Targets.DeregisterTarget(ctx, infra.DeregisterLBRequest(target)); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if err := c.waitNotAccepting(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

// FinishTargetWithdrawal confirms every instance target is absent.
func (c *ProviderCutover) FinishTargetWithdrawal(ctx context.Context, tenant string) error {
	targets, err := c.instanceTargets(tenant)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := c.waitState(ctx, target, infra.LBTargetDeregistered); err != nil {
			return err
		}
	}
	return nil
}

// RestoreTargets registers all currently available instance targets.
func (c *ProviderCutover) RestoreTargets(ctx context.Context, tenant string) error {
	targets, err := c.instanceTargets(tenant)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := c.options.Targets.RegisterTarget(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

// WaitTargetsReady waits for at least one healthy instance target for
// each provider load balancer, registering newly created targets as they appear.
func (c *ProviderCutover) WaitTargetsReady(ctx context.Context, tenant string) error {
	ticker := time.NewTicker(c.options.PollInterval)
	defer ticker.Stop()
	for {
		remaining := make(map[string]bool)
		for _, item := range c.loadBalancers(tenant) {
			name, lb := item.name, item.config
			if lb.Provider != "tunnel" {
				remaining[name] = true
			}
		}
		targets, err := c.instanceTargets(tenant)
		if err != nil {
			return err
		}
		for _, target := range targets {
			state, err := c.options.Provider.GetLBTargetState(ctx, target)
			if err != nil {
				return err
			}
			if state == infra.LBTargetHealthy {
				for _, item := range c.loadBalancers(tenant) {
					name, lb := item.name, item.config
					if sameLB(target.LBConfig, infra.LoadBalancerConfigForProvider(lb)) {
						delete(remaining, name)
					}
				}
			}
		}
		if len(remaining) == 0 {
			return c.waitTunnelUpstreamReady(ctx, tenant)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// RemoveWakePath drains wake targets and stops wake tunnels only after instance
// targets are healthy.
func (c *ProviderCutover) RemoveWakePath(ctx context.Context, tenant string) error {
	for _, item := range c.loadBalancers(tenant) {
		name, lb := item.name, item.config
		if lb.Provider == "tunnel" {
			controller := c.tunnel()
			if controller == nil {
				return fmt.Errorf("wake tunnel %s is unavailable", name)
			}
			revision, err := controller.SetDesired(name, proto.TunnelDesiredState_TUNNEL_DESIRED_STATE_STOPPED)
			if err != nil {
				return err
			}
			status, err := controller.Wait(ctx, name, revision)
			if err != nil {
				return fmt.Errorf("stop wake tunnel %s: %w", name, err)
			}
			if status.GetState() != proto.TunnelStatus_TUNNEL_STATUS_STATE_STOPPED {
				return fmt.Errorf("stop wake tunnel %s: unexpected state %s", name, status.GetState())
			}
			continue
		}
		req := infra.RegisterLBRequest{ProviderInstanceID: c.options.ServerInstanceID, LBConfig: infra.LoadBalancerConfigForProvider(lb), Zone: c.options.Config().Shard.Infra.Zone, WakeProxy: true}
		if err := c.options.Targets.DeregisterTarget(ctx, infra.DeregisterLBRequest(req)); err != nil {
			return err
		}
		if err := c.waitState(ctx, req, infra.LBTargetDeregistered); err != nil {
			return err
		}
	}
	return nil
}

// CheckActivity requires fresh, error-free eBPF observations and reports busy
// when any configured listener has active connections.
func (c *ProviderCutover) CheckActivity(ctx context.Context, tenant string, withdrawalAt time.Time) error {
	ticker := time.NewTicker(c.options.PollInterval)
	defer ticker.Stop()
	for {
		fresh := true
		cfg := c.options.Config()
		for groupName, group := range cfg.Groups[tenant] {
			if group.Size == nil || *group.Size <= 0 {
				continue
			}
			ports := make(map[uint32]bool)
			for _, lbName := range group.LoadBalancers {
				lb := cfg.LoadBalancers[lbName]
				for _, target := range lb.TargetGroups {
					ports[uint32(target.TargetPort)] = true
				}
				for _, listener := range lb.Listeners {
					ports[uint32(listener.TargetPort)] = true
				}
				for _, frontend := range lb.Frontends {
					ports[uint32(frontend.Port)] = true
				}
			}
			if len(ports) == 0 {
				continue
			}
			ids, err := c.options.Instances.GetInstancesByGroup(tenant, groupName, true)
			if err != nil {
				return err
			}
			for _, id := range ids {
				instance, err := c.options.Instances.GetInstance(id)
				if err != nil {
					return err
				}
				if instance.HealthAt == nil || !instance.HealthAt.After(withdrawalAt) {
					fresh = false
					continue
				}
				var report struct {
					Metrics *proto.Metrics `json:"metrics"`
				}
				if err := json.Unmarshal(instance.Health, &report); err != nil {
					return fmt.Errorf("decode health for instance %s: %w", id, err)
				}
				if report.Metrics == nil {
					return fmt.Errorf("decode health for instance %s: metrics are missing", id)
				}
				if report.Metrics.EbpfError != nil {
					return fmt.Errorf("eBPF activity unavailable for instance %s: %s", id, *report.Metrics.EbpfError)
				}
				for port := range ports {
					count, ok := report.Metrics.EbpfCounters[port]
					if !ok {
						return fmt.Errorf("eBPF activity for port %d unavailable on instance %s", port, id)
					}
					if count > 0 {
						return ErrBusy
					}
				}
			}
		}
		if fresh {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// loadBalancers returns load balancers referenced by a tenant's groups.
func (c *ProviderCutover) loadBalancers(tenant string) []namedLoadBalancer {
	cfg := c.options.Config()
	set := make(map[string]config.LoadBalancerConfig)
	for _, group := range cfg.Groups[tenant] {
		if group.Size == nil || *group.Size <= 0 {
			continue
		}
		for _, name := range group.LoadBalancers {
			set[name] = cfg.LoadBalancers[name]
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]namedLoadBalancer, 0, len(names))
	for _, name := range names {
		result = append(result, namedLoadBalancer{name: name, config: set[name]})
	}
	return result
}

// instanceTargets returns stable, de-duplicated provider target requests.
func (c *ProviderCutover) instanceTargets(tenant string) ([]infra.RegisterLBRequest, error) {
	cfg := c.options.Config()
	seen := make(map[string]bool)
	var result []infra.RegisterLBRequest
	groupNames := make([]string, 0, len(cfg.Groups[tenant]))
	for name := range cfg.Groups[tenant] {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)
	for _, groupName := range groupNames {
		group := cfg.Groups[tenant][groupName]
		if group.Size == nil || *group.Size <= 0 {
			continue
		}
		ids, err := c.options.Instances.GetProviderIDsByGroup(tenant, groupName, true)
		if err != nil {
			return nil, fmt.Errorf("list provider targets for group %s: %w", groupName, err)
		}
		for _, lbName := range group.LoadBalancers {
			lb := cfg.LoadBalancers[lbName]
			if lb.Provider == "tunnel" {
				continue
			}
			for _, id := range ids {
				key := lbName + "\x00" + id
				if seen[key] {
					continue
				}
				seen[key] = true
				result = append(result, infra.RegisterLBRequest{ProviderInstanceID: id, LBConfig: infra.LoadBalancerConfigForProvider(lb), Zone: cfg.Shard.Infra.Zone})
			}
		}
	}
	return result, nil
}

// waitNotAccepting waits until the provider has stopped selecting a target for new traffic.
func (c *ProviderCutover) waitNotAccepting(ctx context.Context, request infra.RegisterLBRequest) error {
	ticker := time.NewTicker(c.options.PollInterval)
	defer ticker.Stop()
	for {
		state, err := c.options.Provider.GetLBTargetState(ctx, request)
		if err != nil {
			return err
		}
		if state == infra.LBTargetDraining || state == infra.LBTargetDeregistered {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// waitTunnelUpstreamReady waits for one fresh, reachable upstream per tunnel listener.
func (c *ProviderCutover) waitTunnelUpstreamReady(ctx context.Context, tenant string) error {
	cfg := c.options.Config()
	proxyConfig, err := cfg.ProxyConfig()
	if err != nil {
		return err
	}
	ticker := time.NewTicker(c.options.PollInterval)
	defer ticker.Stop()
	dialer := net.Dialer{}
	for {
		remaining := 0
		cutoff := time.Now().UTC().Add(-c.options.FreshFor)
		for _, item := range c.loadBalancers(tenant) {
			if item.config.Provider != "tunnel" {
				continue
			}
			for _, configured := range item.config.Listeners {
				listener := proxyConfig.Listeners[fmt.Sprintf("%s:%d", item.name, configured.ProxyPort)]
				ready := false
				for _, group := range listener.Groups {
					groupConfig := cfg.Groups[tenant][group]
					if groupConfig.Size == nil || *groupConfig.Size <= 0 {
						continue
					}
					ids, err := c.options.Instances.GetInstancesByGroup(tenant, group, true)
					if err != nil {
						return err
					}
					for _, id := range ids {
						instance, err := c.options.Instances.GetInstance(id)
						if err != nil {
							return err
						}
						if instance.HealthAt == nil || instance.HealthAt.Before(cutoff) {
							continue
						}
						host := ""
						if instance.IP4 != nil {
							host = *instance.IP4
						} else if instance.IP6 != nil {
							host = *instance.IP6
						}
						if host == "" {
							continue
						}
						conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(listener.TargetPort)))
						if err == nil {
							_ = conn.Close()
							ready = true
							break
						}
					}
					if ready {
						break
					}
				}
				if !ready {
					remaining++
				}
			}
		}
		if remaining == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// waitState polls until a provider reports the exact safe target state.
func (c *ProviderCutover) waitState(ctx context.Context, request infra.RegisterLBRequest, wanted infra.LBTargetState) error {
	ticker := time.NewTicker(c.options.PollInterval)
	defer ticker.Stop()
	for {
		state, err := c.options.Provider.GetLBTargetState(ctx, request)
		if err != nil {
			return err
		}
		if state == wanted {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// tunnel returns the current leadership term's tunnel controller.
func (c *ProviderCutover) tunnel() TunnelController {
	if c.options.Tunnel == nil {
		return nil
	}
	return c.options.Tunnel()
}

// sameLB reports whether two provider configurations identify the same resources.
func sameLB(left, right infra.LoadBalancerConfig) bool {
	return left.Provider == right.Provider && slices.Equal(left.TargetGroups, right.TargetGroups) && slices.Equal(left.NetworkEndpointGroups, right.NetworkEndpointGroups)
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package infra

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/infra/provider"
	"github.com/nstance-dev/nstance/internal/server/localdb"
)

// LoadBalancerConfigForProvider converts server load balancer configuration for provider calls.
func LoadBalancerConfigForProvider(lbConfig config.LoadBalancerConfig) LoadBalancerConfig {
	providerConfig := LoadBalancerConfig{
		Provider:              lbConfig.Provider,
		NetworkEndpointGroups: append([]string(nil), lbConfig.NetworkEndpointGroups...),
	}

	providerConfig.TargetGroups = make([]provider.AWSTargetGroupConfig, len(lbConfig.TargetGroups))
	for i, targetGroup := range lbConfig.TargetGroups {
		providerConfig.TargetGroups[i] = provider.AWSTargetGroupConfig{
			ARN:          targetGroup.ARN,
			ListenerPort: targetGroup.ListenerPort,
			TargetPort:   targetGroup.TargetPort,
			ProxyPort:    targetGroup.ProxyPort,
		}
	}

	providerConfig.Frontends = make([]provider.GoogleNLBFrontendConfig, len(lbConfig.Frontends))
	for i, frontend := range lbConfig.Frontends {
		providerConfig.Frontends[i] = provider.GoogleNLBFrontendConfig{
			IP:   frontend.IP,
			Port: frontend.Port,
		}
	}

	providerConfig.Listeners = make([]provider.TunnelListenerConfig, len(lbConfig.Listeners))
	for i, listener := range lbConfig.Listeners {
		providerConfig.Listeners[i] = provider.TunnelListenerConfig{
			TargetPort: listener.TargetPort,
			ProxyPort:  listener.ProxyPort,
		}
	}

	return providerConfig
}

// ValidateLoadBalancers validates all configured load balancers on leader election
// It queries each LB group via provider APIs and warms the SQLite cache with existing associations
func ValidateLoadBalancers(ctx context.Context, cfg *config.Config, localDB *localdb.DB, provider Provider, logger *slog.Logger) error {
	logger.Info("Validating load balancers")

	for lbKey, lbConfig := range cfg.LoadBalancers {
		logger.Info("Validating load balancer",
			"lb_key", lbKey,
			"provider", lbConfig.Provider)

		if lbConfig.Provider == "tunnel" {
			logger.Debug("Skipping provider validation for tunnel load balancer", "lb_key", lbKey)
			continue
		}
		req := ListLBInstancesRequest{
			LBConfig: LoadBalancerConfigForProvider(lbConfig),
			Zone:     cfg.Shard.Infra.Zone,
		}

		instanceIDs, err := provider.ListLBInstances(ctx, req)
		if err != nil {
			logger.Error("Failed to list instances in load balancer",
				"lb_key", lbKey,
				"error", err)
			return fmt.Errorf("validating load balancer %s: %w", lbKey, err)
		}

		logger.Info("Load balancer validated",
			"lb_key", lbKey,
			"instance_count", len(instanceIDs))

		for _, providerInstanceID := range instanceIDs {
			instance, err := localDB.GetInstanceByProviderID(providerInstanceID)
			if err != nil {
				logger.Warn("Failed to find instance for provider ID",
					"provider_instance_id", providerInstanceID,
					"lb_key", lbKey,
					"error", err)
				continue
			}

			if instance != nil {
				if err := localDB.UpsertLBInstance(lbKey, instance.ID, localdb.LBStatusRegistered); err != nil {
					logger.Error("Failed to warm cache for lb_instance",
						"instance_id", instance.ID,
						"lb_key", lbKey,
						"error", err)
				} else {
					logger.Debug("Warmed cache for lb_instance",
						"instance_id", instance.ID,
						"lb_key", lbKey)
				}
			}
		}
	}

	logger.Info("Load balancers validated successfully")
	return nil
}

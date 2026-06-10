// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package infra

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/localdb"
)

// ValidateLoadBalancers validates all configured load balancers on leader election
// It queries each LB group via provider APIs and warms the SQLite cache with existing associations
func ValidateLoadBalancers(ctx context.Context, cfg *config.Config, localDB *localdb.DB, provider Provider, logger *slog.Logger) error {
	logger.Info("Validating load balancers")

	for lbKey, lbConfig := range cfg.LoadBalancers {
		logger.Info("Validating load balancer",
			"lb_key", lbKey,
			"provider", lbConfig.Provider)

		req := ListLBInstancesRequest{
			LBConfig: LoadBalancerConfig{
				Provider:          lbConfig.Provider,
				TargetGroupArns:   lbConfig.TargetGroupArns,
				InstanceGroupName: lbConfig.InstanceGroupName,
			},
			Zone: cfg.Shard.Infra.Zone,
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

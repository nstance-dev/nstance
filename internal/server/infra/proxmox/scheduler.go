// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"fmt"

	proxmox "github.com/luthermonson/go-proxmox"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
	"github.com/nstance-dev/nstance/pkg/topsis"
)

// selectNode chooses the optimal Proxmox node for VM placement using the TOPSIS
// multi-criteria decision analysis algorithm. It evaluates all online nodes based
// on available CPU and memory resources, ranking them to select the node with the
// most capacity. Returns an error if no online nodes are available.
func (p *Provider) selectNode(ctx context.Context, req provider.CreateInstanceRequest) (*proxmox.Node, error) {
	cluster, err := p.client.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	resources, err := cluster.Resources(ctx, "node")
	if err != nil {
		return nil, fmt.Errorf("failed to get node resources: %w", err)
	}

	if len(resources) == 0 {
		return nil, fmt.Errorf("no nodes available in cluster")
	}

	alternatives := make([]topsis.Alternative, 0, len(resources))
	nodeNames := make(map[string]string)

	for _, res := range resources {
		if res.Type != "node" {
			continue
		}

		if res.Status != "online" {
			p.logger.Debug("skipping offline node", "node", res.Node)
			continue
		}

		if res.MaxCPU == 0 || res.MaxMem == 0 {
			p.logger.Warn("skipping node with zero capacity", "node", res.Node)
			continue
		}

		cpuUsage := res.CPU * 100
		memUsage := (float64(res.Mem) / float64(res.MaxMem)) * 100

		availCPU := 100.0 - cpuUsage
		availMem := 100.0 - memUsage

		alternatives = append(alternatives, topsis.Alternative{
			ID:      res.Node,
			Metrics: []float64{availCPU, availMem},
		})

		nodeNames[res.Node] = res.Node

		p.logger.Debug("node resource availability",
			"node", res.Node,
			"cpu_avail", availCPU,
			"mem_avail", availMem,
		)
	}

	if len(alternatives) == 0 {
		return nil, fmt.Errorf("no online nodes available")
	}

	var selectedNodeName string
	if len(alternatives) == 1 {
		selectedNodeName = alternatives[0].ID
	} else {
		weights := topsis.Weights{1.0, 1.0}
		benefits := topsis.BenefitFlags{true, true}

		results, err := topsis.Rank(alternatives, weights, benefits)
		if err != nil {
			return nil, fmt.Errorf("failed to rank nodes: %w", err)
		}

		bestScore := -1.0
		for _, r := range results {
			if r.Score > bestScore {
				bestScore = r.Score
				selectedNodeName = r.ID
			}
		}

		p.logger.Info("selected node for VM placement",
			"node", selectedNodeName,
			"score", bestScore,
		)
	}

	selectedNode, err := p.client.Node(ctx, selectedNodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get selected node: %w", err)
	}

	return selectedNode, nil
}

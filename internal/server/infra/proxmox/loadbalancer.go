// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"fmt"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// SetLBCrossZone reports that cross-zone load balancing is unsupported.
func (p *Provider) SetLBCrossZone(ctx context.Context, req provider.SetLBCrossZoneRequest) error {
	return fmt.Errorf("SetLBCrossZone not implemented for Proxmox")
}

// RegisterWithLB reports that Proxmox load-balancer registration is unsupported.
func (p *Provider) RegisterWithLB(ctx context.Context, req provider.RegisterLBRequest) error {
	return fmt.Errorf("RegisterWithLB not implemented for Proxmox")
}

// DeregisterFromLB reports that Proxmox load-balancer deregistration is unsupported.
func (p *Provider) DeregisterFromLB(ctx context.Context, req provider.DeregisterLBRequest) error {
	return fmt.Errorf("DeregisterFromLB not implemented for Proxmox")
}

// GetLBTargetState reports that Proxmox load-balancer state inspection is unsupported.
func (p *Provider) GetLBTargetState(ctx context.Context, req provider.RegisterLBRequest) (provider.LBTargetState, error) {
	return "", fmt.Errorf("GetLBTargetState not implemented for Proxmox")
}

// ListLBInstances reports that Proxmox load-balancer membership is unsupported.
func (p *Provider) ListLBInstances(ctx context.Context, req provider.ListLBInstancesRequest) ([]string, error) {
	return nil, fmt.Errorf("ListLBInstances not implemented for Proxmox")
}

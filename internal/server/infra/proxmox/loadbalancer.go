// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"fmt"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

func (p *Provider) RegisterWithLB(ctx context.Context, req provider.RegisterLBRequest) error {
	return fmt.Errorf("RegisterWithLB not implemented for Proxmox")
}

func (p *Provider) DeregisterFromLB(ctx context.Context, req provider.DeregisterLBRequest) error {
	return fmt.Errorf("DeregisterFromLB not implemented for Proxmox")
}

func (p *Provider) ListLBInstances(ctx context.Context, req provider.ListLBInstancesRequest) ([]string, error) {
	return nil, fmt.Errorf("ListLBInstances not implemented for Proxmox")
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"fmt"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

func (p *Provider) AssignLeaderNetwork(ctx context.Context, providerInstanceID string, ln provider.LeaderNetwork) error {
	return fmt.Errorf("AssignLeaderNetwork not implemented for Proxmox")
}

func (p *Provider) ReleaseLeaderNetwork(ctx context.Context, providerInstanceID string, ln provider.LeaderNetwork) error {
	return fmt.Errorf("ReleaseLeaderNetwork not implemented for Proxmox")
}

func (p *Provider) CheckSubnetCapacity(ctx context.Context, subnetID string) (bool, error) {
	return true, nil
}

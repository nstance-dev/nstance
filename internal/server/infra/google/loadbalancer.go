// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"context"
	"fmt"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// RegisterWithLB returns an error until GCE_VM_IP NEG membership is implemented.
func (p *Provider) RegisterWithLB(_ context.Context, _ provider.RegisterLBRequest) error {
	return fmt.Errorf("google cloud NEG membership registration is not implemented")
}

// DeregisterFromLB returns an error until GCE_VM_IP NEG membership is implemented.
func (p *Provider) DeregisterFromLB(_ context.Context, _ provider.DeregisterLBRequest) error {
	return fmt.Errorf("google cloud NEG membership deregistration is not implemented")
}

// ListLBInstances returns an error until GCE_VM_IP NEG membership is implemented.
func (p *Provider) ListLBInstances(_ context.Context, _ provider.ListLBInstancesRequest) ([]string, error) {
	return nil, fmt.Errorf("google cloud NEG membership listing is not implemented")
}

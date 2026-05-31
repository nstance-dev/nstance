// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package instanceinfo

import (
	"context"

	"github.com/nstance-dev/nstance/pkg/health"
)

// mockProvider is a Provider that returns a static instance identity.
// Used for local dev/test environments where no cloud metadata service is available.
type mockProvider struct {
	serverID string
}

// NewMockProvider creates a Provider that returns the given serverID as the instance identity.
func NewMockProvider(serverID string) Provider {
	return &mockProvider{serverID: serverID}
}

func (m *mockProvider) Kind() string {
	return "mock"
}

func (m *mockProvider) GetInstanceID(_ context.Context) (string, error) {
	return m.serverID, nil
}

func (m *mockProvider) IsSpot(_ context.Context) (bool, error) {
	return false, nil
}

func (m *mockProvider) GetTerminationNotice(_ context.Context) (*health.TerminationNotice, error) {
	return nil, nil
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/internal/proto"
)

// SleepTenant marks a tenant as asleep.
// TODO: Persist durable shard tenant sleep state.
func (s *Service) SleepTenant(context.Context, *proto.SleepTenantRequest) (*proto.SleepTenantResponse, error) {
	return nil, status.Error(codes.Unimplemented, "durable tenant sleep state is not implemented")
}

// WakeTenant marks a tenant as awake and returns a ready upstream.
// TODO: Persist durable shard tenant sleep state and select a ready upstream.
func (s *Service) WakeTenant(context.Context, *proto.WakeTenantRequest) (*proto.WakeTenantResponse, error) {
	return nil, status.Error(codes.Unimplemented, "durable tenant sleep state is not implemented")
}

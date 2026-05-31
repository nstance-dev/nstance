// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
)

// StubOperatorService provides placeholder implementations for OperatorService
type StubOperatorService struct {
	proto.UnimplementedOperatorServiceServer
}

// GetConfigStatus is a placeholder implementation
func (s *StubOperatorService) GetConfigStatus(ctx context.Context, req *emptypb.Empty) (*proto.ConfigStatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "GetConfigStatus not yet implemented")
}

// ListGroups is a placeholder implementation
func (s *StubOperatorService) ListGroups(ctx context.Context, req *emptypb.Empty) (*proto.ListGroupsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "ListGroups not yet implemented")
}

// UpsertGroup is a placeholder implementation
func (s *StubOperatorService) UpsertGroup(ctx context.Context, req *proto.UpsertGroupRequest) (*proto.GroupStatus, error) {
	return nil, status.Errorf(codes.Unimplemented, "UpsertGroup not yet implemented")
}

// DeleteGroup is a placeholder implementation
func (s *StubOperatorService) DeleteGroup(ctx context.Context, req *proto.DeleteGroupRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "DeleteGroup not yet implemented")
}

// CreateInstance is a placeholder implementation
func (s *StubOperatorService) CreateInstance(ctx context.Context, req *proto.CreateInstanceRequest) (*proto.CreateInstanceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "CreateInstance not yet implemented")
}

// DeleteInstance is a placeholder implementation
func (s *StubOperatorService) DeleteInstance(ctx context.Context, req *proto.DeleteInstanceRequest) (*proto.DeleteInstanceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "DeleteInstance not yet implemented")
}

// GetInstanceStatus is a placeholder implementation
func (s *StubOperatorService) GetInstanceStatus(ctx context.Context, req *proto.GetInstanceStatusRequest) (*proto.InstanceStatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "GetInstanceStatus not yet implemented")
}

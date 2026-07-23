// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package operator

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
)

// WatchErrors streams error events (provider errors, config validation failures) to the operator.
func (s *Service) WatchErrors(req *emptypb.Empty, stream proto.OperatorService_WatchErrorsServer) error {
	clientInfo, err := api.GetClientInfo(stream.Context())
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get client info: %v", err)
	}

	tenant := clientInfo.Tenant
	s.streamMu.Lock()
	s.errorsStreams[tenant] = stream
	s.streamMu.Unlock()

	defer func() {
		s.streamMu.Lock()
		if s.errorsStreams[tenant] == stream {
			delete(s.errorsStreams, tenant)
		}
		s.streamMu.Unlock()
	}()

	s.logger.Info("Operator connected to errors stream", "client_id", clientInfo.ClientID, "tenant", tenant)

	<-stream.Context().Done()
	s.logger.Info("Operator disconnected from errors stream", "client_id", clientInfo.ClientID)
	return nil
}

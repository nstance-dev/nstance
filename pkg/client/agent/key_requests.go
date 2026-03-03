// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"io"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nstance-dev/nstance/internal/proto"
)

// KeyRequestHandler is called when the server sends key generation requests
type KeyRequestHandler interface {
	HandleKeyRequest(keyNames []string) error
}

// ReceiveKeyRequests starts receiving key generation requests from the server
func (c *Client) ReceiveKeyRequests(ctx context.Context, handler KeyRequestHandler) error {
	client, err := c.clientConn()
	if err != nil {
		return err
	}

	c.logger.Info("Starting key request stream")

	stream, err := client.ReceiveKeyRequests(ctx, &emptypb.Empty{})
	if err != nil {
		c.logger.Error("Failed to start key request stream", "error", err)
		return err
	}

	for {
		keyRequest, err := stream.Recv()
		if err == io.EOF {
			c.logger.Info("Key request stream closed by server")
			break
		}
		if err != nil {
			c.logger.Error("Error receiving key request", "error", err)
			return err
		}

		// Ensure type assertion to proto.KeyGenerationRequest
		req := (*proto.KeyGenerationRequest)(keyRequest)

		c.logger.Info("Received key generation request",
			"key_count", len(req.GetKeyNames()),
			"keys", req.GetKeyNames())

		if err := handler.HandleKeyRequest(req.GetKeyNames()); err != nil {
			c.logger.Error("Failed to handle key request",
				"error", err,
				"keys", req.GetKeyNames())
			// Continue processing other requests rather than failing the stream
		}
	}

	return nil
}

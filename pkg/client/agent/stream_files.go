// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/nstance-dev/nstance/internal/agent/receiver"
	"google.golang.org/protobuf/types/known/emptypb"
)

// StreamFiles subscribes to server-sent files and writes them using the provided receiver.
func (c *Client) StreamFiles(ctx context.Context, rcvr *receiver.Receiver) error {
	if rcvr == nil {
		return fmt.Errorf("file receiver cannot be nil")
	}

	client, err := c.clientConn()
	if err != nil {
		return err
	}

	stream, err := client.ReceiveFiles(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("failed to open file stream: %w", err)
	}

	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("file stream error: %w", err)
		}

		// Handle config hash update if present
		if msg.GetConfigHash() != "" {
			if err := rcvr.ReceiveConfigHash(msg.GetConfigHash()); err != nil {
				return fmt.Errorf("failed to update config hash: %w", err)
			}
		}

		if msg.GetFilename() == "" || len(msg.GetContent()) == 0 {
			continue
		}

		err = rcvr.ReceiveFiles(map[string][]byte{msg.GetFilename(): msg.GetContent()})
		if err != nil {
			return fmt.Errorf("failed to write streamed file %q: %w", msg.GetFilename(), err)
		}
	}
}

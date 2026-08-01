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

	files := make(map[string][]byte)
	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(files) != 0 {
					return fmt.Errorf("file stream ended during an incomplete patch")
				}
				return nil
			}
			return fmt.Errorf("file stream error: %w", err)
		}

		if msg.GetConfigHash() == "" {
			if msg.GetFilename() == "" {
				return fmt.Errorf("malformed file patch: filename is required")
			}
			if _, exists := files[msg.GetFilename()]; exists {
				return fmt.Errorf("duplicate file %q in patch", msg.GetFilename())
			}
			files[msg.GetFilename()] = append([]byte(nil), msg.GetContent()...)
			continue
		}
		if msg.GetFilename() != "" || len(msg.GetContent()) != 0 || msg.GetLastModified() != nil {
			return fmt.Errorf("malformed file patch: commit message contains file content")
		}
		if err := rcvr.ReceiveFiles(files, msg.GetConfigHash()); err != nil {
			return fmt.Errorf("failed to apply files: %w", err)
		}
		clear(files)
	}
}

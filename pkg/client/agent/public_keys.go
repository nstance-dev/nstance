// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"

	"github.com/nstance-dev/nstance/internal/proto"
)

// SubmitPublicKeys sends generated public keys to the agent service.
// Note that Certificates are returned via the StreamFiles RPC.
func (c *Client) SubmitPublicKeys(ctx context.Context, publicKeys map[string]string) error {
	if len(publicKeys) == 0 {
		return fmt.Errorf("no public keys to submit")
	}

	instanceID := c.config.InstanceID
	if instanceID == "" {
		return fmt.Errorf("instance ID is not configured")
	}

	client, err := c.clientConn()
	if err != nil {
		return err
	}

	submissions := make([]*proto.PublicKeySubmission, 0, len(publicKeys))
	for filename, pemValue := range publicKeys {
		block, _ := pem.Decode([]byte(pemValue))
		if block == nil || block.Type != "PUBLIC KEY" {
			c.logger.Warn("skipping invalid public key", "filename", filename)
			continue
		}

		submissions = append(submissions, &proto.PublicKeySubmission{
			Filename:     filename,
			PublicKeyPem: []byte(base64.StdEncoding.EncodeToString(block.Bytes)),
		})
	}

	if len(submissions) == 0 {
		return fmt.Errorf("no valid public keys to submit")
	}

	callCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	req := &proto.PublicKeysRequest{
		InstanceId: instanceID,
		Keys:       submissions,
	}

	c.logger.Info("submitting public keys for certificate issuance", "count", len(submissions))
	_, err = client.SubmitPublicKeys(callCtx, req)
	if err != nil {
		return fmt.Errorf("failed to submit public keys: %w", err)
	}

	c.logger.Info("public keys submitted successfully", "count", len(submissions))
	c.logger.Info("certificates will be delivered via file stream")

	return nil
}

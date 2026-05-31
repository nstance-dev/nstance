// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"context"
	"crypto/ed25519"
	"fmt"

	"github.com/nstance-dev/nstance/internal/files"
	"github.com/nstance-dev/nstance/internal/proto"
)

// RegisterOperator registers an operator using its nonce and public key, and returns the client cert
func (c *Client) RegisterOperator(ctx context.Context, nonce string, publicKey ed25519.PublicKey) ([]byte, error) {
	// get gRPC client
	svc, err := c.clientConn()
	if err != nil {
		return nil, err
	}

	// prep public key for gRPC call
	pubPEM, _, _, err := files.EncodePem(publicKey, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to encode public key: %w", err)
	}

	// add timeout to call context
	callCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	// build request
	req := &proto.RegisterClientRequest{
		RegistrationNonceJwt: nonce,
		PublicKeyPem:         pubPEM,
	}

	// register and return client cert
	resp, err := svc.RegisterOperator(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to register operator: %w", err)
	}

	return resp.GetClientCertificatePem(), nil
}

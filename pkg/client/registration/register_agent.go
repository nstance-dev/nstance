// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"context"
	"fmt"
	"strings"

	"github.com/nstance-dev/nstance/internal/files"
	"github.com/nstance-dev/nstance/internal/identity"
	"github.com/nstance-dev/nstance/internal/proto"
)

// RegisterAgent registers an agent using its nonce and public key, and stores the returned client cert.
// ipv4 and ipv6 are the agent's private IP addresses to report to the server.
// hostname is the agent's reported hostname.
func (c *Client) RegisterAgent(ctx context.Context, ident *identity.Identity, nonce, ipv4, ipv6, hostname string) error {
	// validate inputs
	if ident == nil {
		return fmt.Errorf("identity cannot be nil")
	}
	nonce = strings.TrimSpace(nonce)
	if nonce == "" {
		return fmt.Errorf("registration nonce is empty")
	}
	if ident.PublicKey == nil {
		return fmt.Errorf("instance public key not available")
	}

	// get gRPC client
	svc, err := c.clientConn()
	if err != nil {
		return err
	}

	// prep public key for gRPC call
	pubPEM, _, _, err := files.EncodePem(*ident.PublicKey, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to encode public key: %w", err)
	}

	// add timeout to call context
	callCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	// build request
	req := &proto.RegisterClientRequest{
		RegistrationNonceJwt: nonce,
		PublicKeyPem:         pubPEM,
		PrivateIpv4:          ipv4,
		PrivateIpv6:          ipv6,
		Hostname:             hostname,
	}

	// register, then store client cert and delete nonce
	resp, err := svc.RegisterAgent(callCtx, req)
	if err != nil {
		return fmt.Errorf("failed to register agent: %w", err)
	}
	if err := ident.StoreClientCertificate(resp.GetClientCertificatePem()); err != nil {
		return fmt.Errorf("failed to store issued certificate: %w", err)
	}
	if err := ident.DeleteNonce(); err != nil {
		return fmt.Errorf("failed to delete nonce file after registration: %w", err)
	}
	return nil
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"sync"

	"github.com/nstance-dev/nstance/internal/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Client provides access to the RegistrationService
type Client struct {
	config Config

	mu     sync.Mutex
	conn   *grpc.ClientConn
	client proto.RegistrationServiceClient
}

// Config holds connection settings for the registration gRPC client
type Config struct {
	ServerAddress string
	ServerCACert  x509.Certificate
}

// NewClient constructs a registration gRPC client wrapper
func NewClient(cfg Config) (*Client, error) {
	return &Client{
		config: cfg,
	}, nil
}

// Close closes the underlying gRPC connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		c.client = nil
		return err
	}

	return nil
}

// clientConn returns the gRPC client connection for any client methods
func (c *Client) clientConn() (proto.RegistrationServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// return previous client connection if exists
	if c.client != nil && c.conn != nil {
		return c.client, nil
	}

	// build TLS config
	host, _, err := net.SplitHostPort(c.config.ServerAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid gRPC address %q: %w", c.config.ServerAddress, err)
	}

	certPool := x509.NewCertPool()
	certPool.AddCert(&c.config.ServerCACert)

	tlsConfig := &tls.Config{
		MinVersion: minTLSVersion,
		ServerName: host,
		RootCAs:    certPool,
	}

	creds := credentials.NewTLS(tlsConfig)

	// create gRPC client and return
	conn, err := grpc.NewClient(
		c.config.ServerAddress,
		[]grpc.DialOption{
			grpc.WithTransportCredentials(creds),
		}...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to registration service: %w", err)
	}
	c.conn = conn
	c.client = proto.NewRegistrationServiceClient(conn)
	return c.client, nil
}

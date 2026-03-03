// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package renewal

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"time"

	"google.golang.org/grpc"

	"github.com/nstance-dev/nstance/internal/proto"
)

// CertificateStore stores renewed certificates.
type CertificateStore interface {
	StoreCertificate(ctx context.Context, certPEM []byte) error
}

// ConnectionProvider provides a gRPC connection to a shard for renewal.
type ConnectionProvider interface {
	// GetConnection returns a gRPC connection to any available shard.
	// It may establish a new connection or return an existing one.
	GetConnection(ctx context.Context) (conn *grpc.ClientConn, shard string, err error)
}

// Config holds the configuration for certificate renewal.
type Config struct {
	CurrentCert *x509.Certificate
	PrivateKey  ed25519.PrivateKey
	Store       CertificateStore
	Connections ConnectionProvider
	Logger      *slog.Logger
}

// Start begins the certificate renewal goroutine.
// It renews at 50% of remaining certificate lifetime, then exits the process.
func Start(ctx context.Context, cfg Config) {
	if cfg.CurrentCert == nil || cfg.PrivateKey == nil || cfg.Store == nil || cfg.Connections == nil {
		if cfg.Logger != nil {
			cfg.Logger.Info("certificate renewal disabled: missing configuration")
		}
		return
	}

	go run(ctx, cfg)
}

func run(ctx context.Context, cfg Config) {
	logger := cfg.Logger

	expiresAt := cfg.CurrentCert.NotAfter
	now := time.Now()

	remainingLifetime := expiresAt.Sub(now)
	if remainingLifetime <= 0 {
		logger.Error("FATAL: certificate has already expired", "expires_at", expiresAt)
		os.Exit(1)
		return
	}

	renewalTime := now.Add(remainingLifetime / 2)
	sleepDuration := time.Until(renewalTime)

	logger.Info("certificate renewal scheduled",
		"expires_at", expiresAt,
		"renewal_at", renewalTime,
		"sleep_duration", sleepDuration)

	select {
	case <-ctx.Done():
		logger.Info("certificate renewal cancelled due to shutdown")
		return
	case <-time.After(sleepDuration):
		logger.Info("starting certificate renewal")
	}

	backoffDuration := time.Minute
	err := renewCertificate(ctx, cfg)
	for err != nil {
		if ctx.Err() != nil {
			logger.Info("certificate renewal cancelled due to shutdown")
			return
		}

		if time.Now().After(expiresAt) {
			logger.Error("FATAL: certificate expired before renewal succeeded", "expires_at", expiresAt, "error", err)
			os.Exit(1)
			return
		}

		timeUntilExpiry := time.Until(expiresAt)
		if backoffDuration > timeUntilExpiry/2 {
			backoffDuration = timeUntilExpiry / 2
		}
		if backoffDuration < time.Second {
			backoffDuration = time.Second
		}

		logger.Error("certificate renewal failed, retrying", "error", err, "backoff", backoffDuration, "expires_at", expiresAt)

		select {
		case <-ctx.Done():
			logger.Info("certificate renewal cancelled due to shutdown")
			return
		case <-time.After(backoffDuration):
		}

		err = renewCertificate(ctx, cfg)
		if err != nil {
			backoffDuration = backoffDuration * 2
			if backoffDuration > 30*time.Minute {
				backoffDuration = 30 * time.Minute
			}
		}
	}

	logger.Info("certificate renewed successfully, exiting for restart")
	os.Exit(1)
}

func renewCertificate(ctx context.Context, cfg Config) error {
	publicKey := cfg.PrivateKey.Public().(ed25519.PublicKey)
	publicKeyDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("failed to marshal public key: %w", err)
	}

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyDER,
	})

	conn, shard, err := cfg.Connections.GetConnection(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}

	cfg.Logger.Info("attempting certificate renewal", "shard", shard)

	client := proto.NewOperatorServiceClient(conn)
	resp, err := client.RenewCertificate(ctx, &proto.RenewCertificateRequest{
		PublicKeyPem: publicKeyPEM,
	})
	if err != nil {
		return fmt.Errorf("shard %s: renewal failed: %w", shard, err)
	}

	cfg.Logger.Info("certificate renewal successful", "shard", shard, "expires_at", resp.ExpiresAt.AsTime())

	if err := cfg.Store.StoreCertificate(ctx, resp.ClientCertificatePem); err != nil {
		return fmt.Errorf("failed to store renewed certificate: %w", err)
	}

	cfg.Logger.Info("renewed certificate stored successfully")
	return nil
}

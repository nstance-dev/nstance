// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/admin/server"
	"github.com/nstance-dev/nstance/internal/admin/service"
	"github.com/nstance-dev/nstance/internal/identity"
	"github.com/nstance-dev/nstance/internal/renewal"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP API server",
	Long: `Start the HTTP API server for remote administration.

The server exposes JSON endpoints equivalent to CLI commands:
  GET  /config/status   - Get configuration status
  POST /config/refresh  - Trigger configuration refresh
  POST /group/scale     - Scale a group
  GET  /health          - Health check endpoint

Example:
  nstance-admin serve --servers "us-west-2a=172.16.0.1:8993" --bind :8080
  nstance-admin serve --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" --bind 127.0.0.1:8080`,
	Run: runServe,
}

var (
	flagServeServers     string
	flagServeIdentityDir string
	flagServeBind        string
)

func init() {
	lflags := serveCmd.Flags()
	lflags.StringVar(&flagServeServers, "servers", "", "Shard servers (format: shard1=host1:port1,shard2=host2:port2)")
	lflags.StringVar(&flagServeIdentityDir, "identity-dir", "", "Directory containing identity files (default: <temp-dir>/cli-operator-identity/)")
	lflags.StringVar(&flagServeBind, "bind", ":8080", "Address to bind the HTTP server")

	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	if flagServeServers == "" {
		fmt.Fprintf(os.Stderr, "Error: --servers is required\n")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := getLogger()

	connector, id, err := newConnector(flagServeServers, flagServeIdentityDir, 30*time.Second, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer connector.Close()

	// Start certificate renewal goroutine (connects lazily when needed)
	renewal.Start(ctx, renewal.Config{
		CurrentCert: id.ClientCert,
		PrivateKey:  *id.PrivateKey,
		Store:       &identityStore{id},
		Connections: connector,
		Logger:      logger.With("component", "renewal"),
	})

	configService := service.NewConfigService(connector)
	groupService := service.NewGroupService(connector)

	srv, err := server.New(server.Config{
		BindAddr:      flagServeBind,
		ConfigService: configService,
		GroupService:  groupService,
		Logger:        logger,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := srv.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Stop(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}
}

// identityStore adapts identity.Identity to renewal.CertificateStore.
type identityStore struct {
	id *identity.Identity
}

func (s *identityStore) StoreCertificate(_ context.Context, certPEM []byte) error {
	return s.id.StoreClientCertificate(certPEM)
}

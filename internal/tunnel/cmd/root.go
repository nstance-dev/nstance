// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/buildvars"
	"github.com/nstance-dev/nstance/internal/tunnel"
)

var (
	storageBackend string
	bucket         string
	shard          string
	prefix         string
	serverSocket   string
	manifestDir    string
	filesDir       string
)

// NewCommand creates the nstance-server tunnel command.
func NewCommand() *cobra.Command {
	command := &cobra.Command{Use: "tunnel", Short: "Run configured Nstance tunnels", RunE: run}
	command.Flags().StringVar(&storageBackend, "storage", "", "Storage backend (s3, gcs, or file)")
	command.Flags().StringVar(&bucket, "bucket", "", "Storage bucket or file path")
	command.Flags().StringVar(&shard, "shard", "", "Shard ID")
	command.Flags().StringVar(&prefix, "prefix", "", "Shard storage prefix")
	command.Flags().StringVar(&serverSocket, "server-socket", "/run/nstance/nstance-tunnel.sock", "Nstance server tunnel-control socket")
	command.Flags().StringVar(&manifestDir, "manifest-dir", "/etc/kubernetes/manifests", "Kubelet static Pod manifest directory")
	command.Flags().StringVar(&filesDir, "files-dir", "/var/lib/nstance/tunnel", "Resolved tunnel files directory")
	return command
}

// run loads tunnel configuration independently and follows leased server intent.
func run(command *cobra.Command, _ []string) error {
	version, err := command.Flags().GetBool("version")
	if err != nil {
		return err
	}
	if version {
		fmt.Printf("nstance-server tunnel %s\n", buildvars.BuildVersion())
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := tunnel.CleanRuntime(manifestDir, filesDir); err != nil {
		return err
	}
	cfg, cleanup, err := tunnel.LoadConfig(ctx, logger, tunnel.BootstrapConfig{Storage: storageBackend, Bucket: bucket, Shard: shard, Prefix: prefix})
	if err != nil {
		return err
	}
	defer cleanup()
	runtime, err := tunnel.NewRuntime(tunnel.RuntimeOptions{
		Tunnels: cfg.Tunnels, Storage: cfg.Storage, Secrets: cfg.Secrets,
		ManifestDir: manifestDir, FilesDir: filesDir,
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	tunnelNames := make([]string, 0, len(cfg.Tunnels))
	for tunnelName := range cfg.Tunnels {
		tunnelNames = append(tunnelNames, tunnelName)
	}
	sort.Strings(tunnelNames)
	service, err := tunnel.NewService(serverSocket, tunnelNames, runtime, logger)
	if err != nil {
		return err
	}
	if err := service.Run(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

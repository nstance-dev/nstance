// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/buildvars"
	"github.com/nstance-dev/nstance/internal/tunnel"
)

var (
	version    bool
	configPath string
)

// NewRootCmd creates the nstance-tunnel command.
func NewRootCmd() *cobra.Command {
	command := &cobra.Command{Use: "nstance-tunnel", Short: "Run locally allowlisted Nstance tunnels", RunE: run}
	command.Flags().BoolVar(&version, "version", false, "Show version information")
	command.Flags().StringVar(&configPath, "config", "/etc/nstance/tunnel.json", "Path to tunnel configuration")
	return command
}

// run loads the static allowlist and serves tunnel lifecycle requests.
func run(_ *cobra.Command, _ []string) error {
	if version {
		fmt.Printf("nstance-tunnel %s\n", buildvars.BuildVersion())
		return nil
	}
	cfg, err := tunnel.LoadConfig(configPath)
	if err != nil {
		return err
	}
	tunnelNames := make([]string, 0, len(cfg.Tunnels))
	for tunnelName := range cfg.Tunnels {
		tunnelNames = append(tunnelNames, tunnelName)
	}
	service, err := tunnel.NewService(tunnelNames, tunnel.NewProcessRunner(cfg.Tunnels), cfg.MinRestartBackoff, cfg.MaxRestartBackoff)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return tunnel.Serve(ctx, cfg.Socket, service)
}

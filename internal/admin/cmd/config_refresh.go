// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/admin/service"
)

var configRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Trigger configuration refresh on Nstance Server(s)",
	Long: `Trigger configuration refresh on one or more Nstance Servers.

The server will reload configuration from S3 and apply changes.

Example:
  nstance-admin config refresh --servers "us-west-2a=172.16.0.1:8993" --shard us-west-2a
  nstance-admin config refresh --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" --all-shards`,
	Run: runConfigRefresh,
}

func init() {
	configCmd.AddCommand(configRefreshCmd)
}

func runConfigRefresh(cmd *cobra.Command, args []string) {
	if flagConfigServers == "" {
		fmt.Fprintf(os.Stderr, "Error: --servers is required\n")
		os.Exit(1)
	}
	if flagConfigShard == "" && !flagConfigAllShards {
		fmt.Fprintf(os.Stderr, "Error: must specify --shard or --all-shards\n")
		os.Exit(1)
	}
	if flagConfigShard != "" && flagConfigAllShards {
		fmt.Fprintf(os.Stderr, "Error: --shard and --all-shards are mutually exclusive\n")
		os.Exit(1)
	}

	timeout, err := time.ParseDuration(flagConfigTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid timeout: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	logger := getLogger()

	connector, _, err := newConnector(flagConfigServers, flagConfigIdentityDir, timeout, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer connector.Close()

	svc := service.NewConfigService(connector)
	resp, err := svc.Refresh(ctx, service.ConfigRefreshRequest{
		Shard:     flagConfigShard,
		AllShards: flagConfigAllShards,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	hasError := false
	for _, shard := range resp.Shards {
		if shard.Error != nil {
			fmt.Fprintf(os.Stderr, "Error refreshing %s: %v\n", shard.Shard, shard.Error)
			hasError = true
			continue
		}
		if shard.Updated {
			fmt.Printf("%s: configuration updated (etag: %s)\n", shard.Shard, shard.Etag)
		} else {
			fmt.Printf("%s: configuration unchanged\n", shard.Shard)
		}
	}

	if hasError {
		os.Exit(1)
	}
}

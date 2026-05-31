// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/admin/service"
)

var configStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check configuration status on Nstance Server(s)",
	Long: `Check configuration status on one or more Nstance Servers.

Shows the current configuration etag and last modified time.

Example:
  nstance-admin config status --servers "us-west-2a=172.16.0.1:8993" --shard us-west-2a
  nstance-admin config status --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" --all-shards`,
	Run: runConfigStatus,
}

func init() {
	configCmd.AddCommand(configStatusCmd)
}

func runConfigStatus(cmd *cobra.Command, args []string) {
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
	resp, err := svc.Status(ctx, service.ConfigStatusRequest{
		Shard:     flagConfigShard,
		AllShards: flagConfigAllShards,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SHARD\tETAG\tLAST_MODIFIED\tSIZE")

	hasError := false
	for _, shard := range resp.Shards {
		if shard.Error != nil {
			fmt.Fprintf(os.Stderr, "Error getting status for %s: %v\n", shard.Shard, shard.Error)
			hasError = true
			continue
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\n",
			shard.Shard,
			shard.Etag,
			shard.LastModified.Format(time.RFC3339),
			shard.Size,
		)
	}
	_ = w.Flush()

	if hasError {
		os.Exit(1)
	}
}

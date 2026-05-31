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

var groupListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all groups",
	Long: `List all groups and their current state.

Example:
  nstance-admin group list --servers "us-west-2a=172.16.0.1:8993" --shard us-west-2a
  nstance-admin group list --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" --all-shards`,
	Args: cobra.NoArgs,
	Run:  runGroupList,
}

func init() {
	groupCmd.AddCommand(groupListCmd)
}

func runGroupList(cmd *cobra.Command, args []string) {
	if flagGroupServers == "" {
		fmt.Fprintf(os.Stderr, "Error: --servers is required\n")
		os.Exit(1)
	}
	if flagGroupShard == "" && !flagGroupAllShards {
		fmt.Fprintf(os.Stderr, "Error: must specify --shard or --all-shards\n")
		os.Exit(1)
	}
	if flagGroupShard != "" && flagGroupAllShards {
		fmt.Fprintf(os.Stderr, "Error: --shard and --all-shards are mutually exclusive\n")
		os.Exit(1)
	}

	timeout, err := time.ParseDuration(flagGroupTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid timeout: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	logger := getLogger()

	connector, _, err := newConnector(flagGroupServers, flagGroupIdentityDir, timeout, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer connector.Close()

	svc := service.NewGroupService(connector)
	resp, err := svc.List(ctx, service.GroupListRequest{
		Shard:     flagGroupShard,
		AllShards: flagGroupAllShards,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	hasError := false
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "SHARD\tGROUP\tSIZE\tTEMPLATE\tSTATIC\n")

	for _, result := range resp.Results {
		if result.Error != nil {
			fmt.Fprintf(os.Stderr, "Error on %s: %v\n", result.Shard, result.Error)
			hasError = true
			continue
		}
		staticStr := "no"
		if result.IsStatic {
			staticStr = "yes"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", result.Shard, result.Key, result.Size, result.Template, staticStr)
	}
	_ = w.Flush()

	if hasError {
		os.Exit(1)
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
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

var groupDeleteCmd = &cobra.Command{
	Use:   "delete <group>",
	Short: "Delete a dynamic group",
	Long: `Delete a dynamic group.

For static groups (defined in server config), this removes any dynamic overrides
but cannot fully delete the group. For dynamic groups, this removes the group
entirely.

Example:
  nstance-admin group delete my-group --servers "us-west-2a=172.16.0.1:8993" --shard us-west-2a
  nstance-admin group delete my-group --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" --all-shards`,
	Args: cobra.ExactArgs(1),
	Run:  runGroupDelete,
}

func init() {
	groupCmd.AddCommand(groupDeleteCmd)
}

func runGroupDelete(cmd *cobra.Command, args []string) {
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

	groupKey := args[0]

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
	resp, err := svc.Delete(ctx, service.GroupDeleteRequest{
		Shard:     flagGroupShard,
		AllShards: flagGroupAllShards,
		Group:     groupKey,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	hasError := false
	for _, result := range resp.Results {
		if result.Error != nil {
			fmt.Fprintf(os.Stderr, "Error deleting %s on %s: %v\n", result.Group, result.Shard, result.Error)
			hasError = true
			continue
		}
		fmt.Printf("%s: deleted group %s\n", result.Shard, result.Group)
	}

	if hasError {
		os.Exit(1)
	}
}

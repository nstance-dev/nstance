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

var groupScaleCmd = &cobra.Command{
	Use:   "scale <group> <size>",
	Short: "Scale a group to a specified size",
	Long: `Scale a group to a specified size by updating the group's size field.

This command updates only the size field of an existing group, leaving all other
configuration unchanged. The group must already exist in either static or dynamic
configuration.

Example:
  nstance-admin group scale my-group 5 --servers "us-west-2a=172.16.0.1:8993" --shard us-west-2a
  nstance-admin group scale my-group 10 --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" --all-shards`,
	Args: cobra.ExactArgs(2),
	Run:  runGroupScale,
}

func init() {
	groupCmd.AddCommand(groupScaleCmd)
}

func runGroupScale(cmd *cobra.Command, args []string) {
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
	var size int
	if _, err := fmt.Sscanf(args[1], "%d", &size); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid size: %v\n", err)
		os.Exit(1)
	}
	if size < 0 {
		fmt.Fprintf(os.Stderr, "Error: size must be non-negative\n")
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
	resp, err := svc.Scale(ctx, service.GroupScaleRequest{
		Shard:     flagGroupShard,
		AllShards: flagGroupAllShards,
		Group:     groupKey,
		Size:      int32(size),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	hasError := false
	for _, result := range resp.Results {
		if result.Error != nil {
			fmt.Fprintf(os.Stderr, "Error scaling %s on %s: %v\n", result.Group, result.Shard, result.Error)
			hasError = true
			continue
		}
		fmt.Printf("%s: scaled group %s to size %d\n", result.Shard, result.Group, result.Size)
	}

	if hasError {
		os.Exit(1)
	}
}

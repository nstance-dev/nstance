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

var groupStatusCmd = &cobra.Command{
	Use:   "status <group>",
	Short: "Show detailed status of a group",
	Long: `Show detailed status of a specific group.

Example:
  nstance-admin group status my-group --servers "us-west-2a=172.16.0.1:8993" --shard us-west-2a
  nstance-admin group status my-group --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" --all-shards`,
	Args: cobra.ExactArgs(1),
	Run:  runGroupStatus,
}

func init() {
	groupCmd.AddCommand(groupStatusCmd)
}

func runGroupStatus(cmd *cobra.Command, args []string) {
	groupName := args[0]

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
	resp, err := svc.Status(ctx, service.GroupStatusRequest{
		Shard:     flagGroupShard,
		AllShards: flagGroupAllShards,
		Group:     groupName,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	hasError := false
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

		fmt.Printf("Shard: %s\n", result.Shard)
		fmt.Printf("Group: %s\n", result.Key)
		fmt.Printf("Template: %s\n", result.Template)
		fmt.Printf("Size: %d\n", result.Size)
		fmt.Printf("Static: %s\n", staticStr)
		if result.InstanceType != "" {
			fmt.Printf("Instance Type: %s\n", result.InstanceType)
		}
		if result.SubnetPool != "" {
			fmt.Printf("Subnet Pool: %s\n", result.SubnetPool)
		}
		if len(result.Vars) > 0 {
			fmt.Printf("Vars:\n")
			for k, v := range result.Vars {
				fmt.Printf("  %s: %s\n", k, v)
			}
		}
		fmt.Println()
	}

	if hasError {
		os.Exit(1)
	}
}

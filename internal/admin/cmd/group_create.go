// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/admin/service"
)

var (
	flagGroupCreateTemplate     string
	flagGroupCreateSize         int
	flagGroupCreateInstanceType string
	flagGroupCreateSubnetPool   string
	flagGroupCreateVars         []string
)

var groupCreateCmd = &cobra.Command{
	Use:   "create <group>",
	Short: "Create a new dynamic group",
	Long: `Create a new dynamic group with the specified configuration.

Example:
  nstance-admin group create my-group --servers "us-west-2a=172.16.0.1:8993" --shard us-west-2a --template knc --size 2
  nstance-admin group create my-group --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" --all-shards --template knc --size 4`,
	Args: cobra.ExactArgs(1),
	Run:  runGroupCreate,
}

func init() {
	groupCreateCmd.Flags().StringVar(&flagGroupCreateTemplate, "template", "", "Instance template to use (required)")
	groupCreateCmd.Flags().IntVar(&flagGroupCreateSize, "size", 0, "Initial group size")
	groupCreateCmd.Flags().StringVar(&flagGroupCreateInstanceType, "instance-type", "", "Instance type override")
	groupCreateCmd.Flags().StringVar(&flagGroupCreateSubnetPool, "subnet-pool", "", "Subnet pool override")
	groupCreateCmd.Flags().StringSliceVar(&flagGroupCreateVars, "var", nil, "Variable overrides (key=value)")

	_ = groupCreateCmd.MarkFlagRequired("template")

	groupCmd.AddCommand(groupCreateCmd)
}

func runGroupCreate(cmd *cobra.Command, args []string) {
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

	vars := make(map[string]string)
	for _, v := range flagGroupCreateVars {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "Error: invalid var format %q, expected key=value\n", v)
			os.Exit(1)
		}
		vars[parts[0]] = parts[1]
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
	resp, err := svc.Create(ctx, service.GroupCreateRequest{
		Shard:        flagGroupShard,
		AllShards:    flagGroupAllShards,
		Group:        groupKey,
		Template:     flagGroupCreateTemplate,
		Size:         int32(flagGroupCreateSize),
		InstanceType: flagGroupCreateInstanceType,
		SubnetPool:   flagGroupCreateSubnetPool,
		Vars:         vars,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	hasError := false
	for _, result := range resp.Results {
		if result.Error != nil {
			fmt.Fprintf(os.Stderr, "Error creating %s on %s: %v\n", result.Group, result.Shard, result.Error)
			hasError = true
			continue
		}
		fmt.Printf("%s: created group %s with size %d\n", result.Shard, result.Group, result.Size)
	}

	if hasError {
		os.Exit(1)
	}
}

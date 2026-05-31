// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/spf13/cobra"
)

var groupCmd = &cobra.Command{
	Use:   "group",
	Short: "Manage Nstance groups",
	Long:  `Commands for managing Nstance groups.`,
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

var (
	flagGroupServers     string
	flagGroupIdentityDir string
	flagGroupShard       string
	flagGroupAllShards   bool
	flagGroupTimeout     string
)

func init() {
	pflags := groupCmd.PersistentFlags()
	pflags.StringVar(&flagGroupServers, "servers", "", "Shard servers (format: shard1=host1:port1,shard2=host2:port2)")
	pflags.StringVar(&flagGroupIdentityDir, "identity-dir", "", "Directory containing identity files (default: <temp-dir>/cli-operator-identity/)")
	pflags.StringVar(&flagGroupShard, "shard", "", "Target a specific shard")
	pflags.BoolVar(&flagGroupAllShards, "all-shards", false, "Target all shards in the servers list")
	pflags.StringVar(&flagGroupTimeout, "timeout", "30s", "Timeout for operations")

	rootCmd.AddCommand(groupCmd)
}

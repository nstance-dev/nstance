// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage Nstance Server configuration",
	Long:  `Commands for managing Nstance Server configuration.`,
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

var (
	flagConfigServers     string
	flagConfigIdentityDir string
	flagConfigShard       string
	flagConfigAllShards   bool
	flagConfigTimeout     string
)

func init() {
	pflags := configCmd.PersistentFlags()
	pflags.StringVar(&flagConfigServers, "servers", "", "Shard servers (format: shard1=host1:port1,shard2=host2:port2)")
	pflags.StringVar(&flagConfigIdentityDir, "identity-dir", "", "Directory containing identity files (default: <temp-dir>/cli-operator-identity/)")
	pflags.StringVar(&flagConfigShard, "shard", "", "Target a specific shard")
	pflags.BoolVar(&flagConfigAllShards, "all-shards", false, "Target all shards in the servers list")
	pflags.StringVar(&flagConfigTimeout, "timeout", "30s", "Timeout for operations")

	rootCmd.AddCommand(configCmd)
}

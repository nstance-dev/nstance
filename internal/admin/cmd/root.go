// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/buildvars"
)

var rootCmd = &cobra.Command{
	Use:   "nstance-admin",
	Short: "Nstance Admin",
	Long:  `Command-line and HTTP API tools for managing Nstance clusters.`,
}

var (
	flagDebug   bool
	flagVersion bool
	flagTempDir string
)

func init() {
	pflags := rootCmd.PersistentFlags()
	pflags.BoolVarP(&flagDebug, "debug", "v", false, "Enable debug output")
	pflags.StringVar(&flagTempDir, "temp-dir", "./temp", "Temp directory for identity and cache files")
	if flag := pflags.Lookup("debug"); flag != nil {
		flag.NoOptDefVal = "true"
	}

	lflags := rootCmd.Flags()
	lflags.BoolVar(&flagVersion, "version", false, "Show version information")
}

func Execute() error {
	rootCmd.Run = func(cmd *cobra.Command, args []string) {
		if flagVersion {
			fmt.Printf("nstance-admin %s\n", buildvars.BuildVersion())
			if flagDebug {
				fmt.Printf("build version: %s\n", buildvars.BuildVersion())
				fmt.Printf("build date: %s\n", buildvars.BuildDate())
				fmt.Printf("commit hash: %s\n", buildvars.CommitHash())
				fmt.Printf("commit date: %s\n", buildvars.CommitDate())
				fmt.Printf("commit branch: %s\n", buildvars.CommitBranch())
			}
			return
		}
		_ = cmd.Help()
	}
	return rootCmd.Execute()
}

func getLogger() *slog.Logger {
	level := slog.LevelInfo
	if flagDebug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

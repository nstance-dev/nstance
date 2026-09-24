// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/nstance-dev/nstance/internal/tunnel/cmd"
)

// main runs the nstance-tunnel command.
func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

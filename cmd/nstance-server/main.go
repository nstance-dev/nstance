// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/nstance-dev/nstance/internal/server/cmd"
)

func main() {
	err := cmd.NewRootCmd().Execute()
	if err != nil {
		os.Exit(1)
	}
}

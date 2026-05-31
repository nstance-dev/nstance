// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"log/slog"
	"path/filepath"
	"time"

	"github.com/nstance-dev/nstance/internal/admin/service"
	"github.com/nstance-dev/nstance/internal/identity"
)

// newConnector creates a Connector from command flags.
// It parses the servers flag, loads identity from identityDir (or default),
// and builds TLS config.
func newConnector(serversFlag, identityDirFlag string, timeout time.Duration, logger *slog.Logger) (*service.Connector, *identity.Identity, error) {
	servers, err := service.ParseServersFlag(serversFlag)
	if err != nil {
		return nil, nil, err
	}

	identityDir := identityDirFlag
	if identityDir == "" {
		identityDir = filepath.Join(flagTempDir, "cli-operator-identity")
	}

	id, err := identity.Load(identityDir, logger)
	if err != nil {
		return nil, nil, err
	}

	tlsConfig, err := id.BuildTLSConfig()
	if err != nil {
		return nil, nil, err
	}

	return service.NewConnector(servers, tlsConfig, timeout, logger), id, nil
}

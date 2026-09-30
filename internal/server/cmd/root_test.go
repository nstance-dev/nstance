// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import "testing"

// TestProxyCommandUsesServerBinary verifies the proxy is available only as an
// nstance-server subcommand with its own runtime flags.
func TestProxyCommandUsesServerBinary(t *testing.T) {
	command, _, err := NewRootCmd().Find([]string{"proxy"})
	if err != nil {
		t.Fatalf("Find(proxy): %v", err)
	}
	if command.Name() != "proxy" {
		t.Fatalf("command name = %q, want proxy", command.Name())
	}
	for _, name := range []string{"socket", "bind-host", "debug", "version"} {
		if command.Flag(name) == nil {
			t.Errorf("proxy command is missing %q flag", name)
		}
	}
	if command.Flag("id") != nil {
		t.Error("proxy command inherited server-only id flag")
	}
}

// TestTunnelCommandUsesServerBinary verifies the privileged tunnel supervisor
// is available as an nstance-server subcommand with independent runtime flags.
func TestTunnelCommandUsesServerBinary(t *testing.T) {
	command, _, err := NewRootCmd().Find([]string{"tunnel"})
	if err != nil {
		t.Fatalf("Find(tunnel): %v", err)
	}
	if command.Name() != "tunnel" {
		t.Fatalf("command name = %q, want tunnel", command.Name())
	}
	for _, name := range []string{"storage", "bucket", "shard", "prefix", "server-socket", "manifest-dir", "files-dir", "debug", "version"} {
		if command.Flag(name) == nil {
			t.Errorf("tunnel command is missing %q flag", name)
		}
	}
	if command.Flag("id") != nil {
		t.Error("tunnel command inherited server-only id flag")
	}
}

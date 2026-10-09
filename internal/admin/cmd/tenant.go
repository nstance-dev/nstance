// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nstance-dev/nstance/v2/internal/admin/service"
	"github.com/nstance-dev/nstance/v2/internal/proto"
)

// tenantConnector is the existing connector surface needed by tenant commands.
type tenantConnector interface {
	ConnectShard(context.Context, string) (*service.Connection, error)
	Servers() []service.ShardServer
	Close()
}

// init registers the CLI-only tenant commands.
func init() {
	rootCmd.AddCommand(newTenantCommand(func(servers, identityDir string, timeout time.Duration) (tenantConnector, error) {
		connector, _, err := newConnector(servers, identityDir, timeout, getLogger())
		return connector, err
	}))
}

// newTenantCommand builds tenant commands with an injectable connector for tests.
func newTenantCommand(connect func(string, string, time.Duration) (tenantConnector, error)) *cobra.Command {
	var servers, identityDir, shard string
	var allShards bool
	var timeout time.Duration
	cmd := &cobra.Command{Use: "tenant", Short: "Inspect, sleep, or wake a tenant on selected shards"}
	flags := cmd.PersistentFlags()
	flags.StringVar(&servers, "servers", os.Getenv(envAdminServers), "Shard servers (format: shard1=host1:port1,shard2=host2:port2)")
	flags.StringVar(&identityDir, "identity-dir", os.Getenv(envAdminIdentityDir), "Directory containing identity files (default: <temp-dir>/cli-operator-identity/)")
	flags.StringVar(&shard, "shard", os.Getenv(envAdminShard), "Target a specific shard")
	flags.BoolVar(&allShards, "all-shards", false, "Target all configured shards (not atomic; no rollback)")
	flags.DurationVar(&timeout, "timeout", 10*time.Minute, "Timeout per shard operation (allows provider drain)")
	for _, operation := range []struct{ name, description string }{
		{"status", "Show tenant sleep state and listener activity"},
		{"sleep", "Sleep a tenant while preserving desired group sizes"},
		{"wake", "Wake a tenant and resume group reconciliation"},
	} {
		var force bool
		var wakeAt string
		sub := &cobra.Command{
			Use: operation.name + " <tenant>", Short: operation.description, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				req := service.TenantRequest{
					Tenant: args[0], Shard: shard, AllShards: allShards, Timeout: timeout, Force: force,
				}
				parsedServers, err := service.ParseServersFlag(servers)
				if err != nil {
					return err
				}
				if cmd.Flags().Changed("wake-at") {
					value, err := time.Parse(time.RFC3339, wakeAt)
					if err != nil {
						return fmt.Errorf("invalid --wake-at (expected RFC3339): %w", err)
					}
					req.WakeAt = &value
				}
				if err := req.Validate(parsedServers); err != nil {
					return err
				}
				cmd.SilenceUsage = true
				connector, err := connect(servers, identityDir, timeout)
				if err != nil {
					return err
				}
				defer connector.Close()
				if force {
					cmd.PrintErrln("Warning: forced sleep may interrupt workloads and active connections.")
				}
				tenantService := service.NewTenantService(connector)
				var results []service.TenantResult
				if operation.name != "status" {
					cmd.PrintErrf("Requesting %s for tenant %s (timeout %s per shard)...\n", operation.name, req.Tenant, req.Timeout)
					if operation.name == "sleep" {
						if force {
							cmd.PrintErrln("Forced sleep waits for wake-proxy readiness and withdrawal from new traffic, but does not wait for existing connections to drain.")
						} else {
							cmd.PrintErrln("Sleep waits for wake-proxy readiness and load-balancer draining before shutting down instances; draining can take several minutes.")
						}
					}
				}
				started := time.Now()
				done := make(chan struct{})
				go func() {
					defer close(done)
					switch operation.name {
					case "status":
						results, err = tenantService.Status(cmd.Context(), req)
					case "sleep":
						results, err = tenantService.Sleep(cmd.Context(), req)
					case "wake":
						results, err = tenantService.Wake(cmd.Context(), req)
					}
				}()
				ticker := time.NewTicker(15 * time.Second)
				defer ticker.Stop()
			wait:
				for {
					select {
					case <-done:
						break wait
					case <-ticker.C:
						if operation.name != "status" {
							cmd.PrintErrf("Still waiting for tenant %s %s (%s elapsed); no completion response yet.\n", req.Tenant, operation.name, time.Since(started).Truncate(time.Second))
						}
					}
				}
				if err != nil {
					return err
				}
				var failures []error
				for _, result := range results {
					if result.Error != nil {
						cmd.SilenceErrors = true // Each shard failure is printed here, not again by Cobra.
						rpcStatus, ok := status.FromError(result.Error)
						if ok && rpcStatus.Code() == codes.Unavailable && strings.Contains(rpcStatus.Message(), "connection error:") {
							var address string
							for _, server := range parsedServers {
								if server.ShardID == result.Shard {
									address = server.Address
									break
								}
							}
							cmd.PrintErrf("%s: Can't connect to server at %s.\n", result.Shard, address)
							cmd.PrintErrln("Check the address and that the server is reachable. If you use port forwarding, start or restart the tunnel.")
							if flagDebug {
								cmd.PrintErrf("Details: %v\n", result.Error)
							} else {
								cmd.PrintErrln("Use --debug for connection details.")
							}
						} else {
							cmd.PrintErrln(result.Error)
						}
						failures = append(failures, result.Error)
						continue
					}
					prefix := fmt.Sprintf("%s: tenant %s", result.Shard, req.Tenant)
					state := "unknown"
					switch result.Status {
					case proto.TenantSleepStatus_TENANT_SLEEP_STATUS_AWAKE:
						state = "awake"
					case proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP:
						state = "asleep"
					}
					outcome := ""
					if result.Result != "" {
						outcome = " (" + result.Result + ")"
					}
					deadline := ""
					if result.WakeAt != nil {
						deadline = " wake-at=" + result.WakeAt.AsTime().Format(time.RFC3339)
					}
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s%s%s\n", prefix, state, outcome, deadline); err != nil {
						failures = append(failures, err)
						continue
					}
					for _, listener := range result.Listeners {
						availability, idle := "unavailable", "unknown"
						if listener.GetAvailable() {
							availability = "available"
						}
						if listener.GetIdleSince() != nil {
							idle = listener.GetIdleSince().AsTime().Format(time.RFC3339)
						}
						if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s listener %s: activity=%s, idle-since=%s\n", prefix, listener.GetListener(), availability, idle); err != nil {
							failures = append(failures, err)
							break
						}
					}
				}
				return errors.Join(failures...)
			},
		}
		if operation.name == "sleep" {
			sub.Flags().BoolVar(&force, "force", false, "Skip activity guard and remaining load-balancer drain; may interrupt workloads and active connections")
			sub.Flags().StringVar(&wakeAt, "wake-at", "", "Optional wake deadline (RFC3339)")
		}
		cmd.AddCommand(sub)
	}
	return cmd
}

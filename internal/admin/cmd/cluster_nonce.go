// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/admin/service"
)

var clusterNonceCmd = &cobra.Command{
	Use:   "nonce",
	Short: "Generate a registration nonce JWT for use with admin or operator",
	Long: `Generate a registration nonce JWT for use with admin or operator.

This command generates a JWT that can be used by nstance-admin or nstance-operator
to register with any Nstance Server in the cluster. When using with nstance-admin,
the JWT should be stored either in the default path or in a custom path (which is later
specified to nstance-admin commands using the -nonce flag). When using with
the nstance-operator, the JWT should be stored in a Kubernetes Secret and provided to 
the operator during cluster bootstrap.

By default, the nonce is written to <temp-dir>/cli-operator-identity/nonce.jwt
for use by other admin commands. Use --output to specify a different path,
or --output - to write to stdout.

Example:
  nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --key-provider env
  nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --key-provider env --tenant prod
  nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --key-provider env --expiry 1h
  nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --key-provider env --output -`,
	RunE: runClusterNonce,
}

var (
	flagClusterNonceClusterID string
	flagClusterNonceTenant    string
	flagClusterNonceExpiry    string
	flagClusterNonceOutput    string
)

func init() {
	flags := clusterNonceCmd.Flags()
	flags.StringVar(&flagClusterNonceClusterID, "cluster-id", "", "Cluster ID (required)")
	flags.StringVar(&flagClusterNonceTenant, "tenant", "default", "Tenant identifier for the operator")
	flags.StringVar(&flagClusterNonceExpiry, "expiry", "3h", "JWT expiry duration (e.g., 30m, 1h, 24h)")
	flags.StringVar(&flagClusterNonceOutput, "output", "", "Output path for nonce JWT (default: <temp-dir>/cli-operator-identity/nonce.jwt, use - for stdout)")

	_ = clusterNonceCmd.MarkFlagRequired("cluster-id")

	clusterCmd.AddCommand(clusterNonceCmd)
}

func runClusterNonce(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	expiry, err := time.ParseDuration(flagClusterNonceExpiry)
	if err != nil {
		return fmt.Errorf("invalid expiry duration: %w", err)
	}

	svc := service.NewNonceService(clusterSecretsStore)
	resp, err := svc.Generate(ctx, service.NonceRequest{
		ClusterID: flagClusterNonceClusterID,
		Tenant:    flagClusterNonceTenant,
		Expiry:    expiry,
	})
	if err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}

	outputPath := flagClusterNonceOutput
	if outputPath == "-" {
		fmt.Println(resp.JWT)
		return nil
	}

	if outputPath == "" {
		outputPath = filepath.Join(flagTempDir, "cli-operator-identity", "nonce.jwt")
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0700); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(outputPath, []byte(resp.JWT), 0600); err != nil {
		return fmt.Errorf("write nonce: %w", err)
	}

	fmt.Printf("Nonce written to %s\n", outputPath)
	return nil
}

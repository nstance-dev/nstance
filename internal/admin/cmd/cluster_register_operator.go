// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/refreshjs/puidv7"
	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/files"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

var clusterRegisterOperatorCmd = &cobra.Command{
	Use:   "register-operator",
	Short: "Register an operator with the cluster",
	Long: `Register an operator with the cluster by creating a client certificate
and storing the registration record in cluster storage.

This command is used when cluster leader election is disabled and operators
cannot be registered via gRPC. It requires:
- Access to cluster storage (for reading CA and writing registration record)
- Access to the encryption key or secrets (for accessing/decrypting the CA private key)

Example:
  nstance-admin cluster register-operator \
    --storage-bucket my-cluster-bucket \
    --secrets-provider object-storage \
    --key-provider env
`,
	RunE: runClusterRegisterOperator,
}

var (
	flagRegisterOperatorTenant        string
	flagRegisterOperatorID            string
	flagRegisterOperatorPublicKeyFile string
	flagRegisterOperatorOutputDir     string
	flagRegisterOperatorCertTTLHours  int
)

func init() {
	flags := clusterRegisterOperatorCmd.Flags()
	flags.StringVar(&flagRegisterOperatorTenant, "tenant", "default", "Tenant name for the operator")
	flags.StringVar(&flagRegisterOperatorID, "operator-id", "", "Operator ID (optional - generates puidv7 with 'opr' prefix if not provided)")
	flags.StringVar(&flagRegisterOperatorPublicKeyFile, "public-key-file", "", "Path to operator's public key file (PEM, optional - generates keypair if not provided)")
	flags.StringVar(&flagRegisterOperatorOutputDir, "output-dir", "", "Directory to write identity files (default: <temp-dir>/cli-operator-identity/)")
	flags.IntVar(&flagRegisterOperatorCertTTLHours, "cert-ttl-hours", 8760, "Certificate TTL in hours (default: 1 year)")

	clusterCmd.AddCommand(clusterRegisterOperatorCmd)
}

func runClusterRegisterOperator(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	operatorID := flagRegisterOperatorID
	if operatorID == "" {
		var err error
		operatorID, err = puidv7.New("opr")
		if err != nil {
			return fmt.Errorf("failed to generate operator ID: %w", err)
		}
	}

	var publicKeyPEM []byte
	var privateKeyPEM []byte

	if flagRegisterOperatorPublicKeyFile != "" {
		var err error
		publicKeyPEM, err = os.ReadFile(flagRegisterOperatorPublicKeyFile)
		if err != nil {
			return fmt.Errorf("failed to read public key file: %w", err)
		}
	} else {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("failed to generate keypair: %w", err)
		}
		publicKeyPEM, privateKeyPEM, _, err = files.EncodePem(publicKey, privateKey, nil)
		if err != nil {
			return fmt.Errorf("failed to encode keypair: %w", err)
		}
	}

	caCertPEM, _, err := clusterStorage.Get(ctx, "secret/ca.crt")
	if err != nil {
		return fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caKeyPEM, err := clusterSecretsStore.Get(ctx, "ca.key")
	if err != nil {
		return fmt.Errorf("failed to read CA private key: %w", err)
	}

	certPEM, expiresAt, err := pki.GenerateClientCertificate(
		caCertPEM, caKeyPEM, publicKeyPEM,
		operatorID, "operator", flagRegisterOperatorTenant,
		flagRegisterOperatorCertTTLHours,
	)
	if err != nil {
		return fmt.Errorf("failed to generate certificate: %w", err)
	}

	certSerial, err := pki.ExtractCertSerial(certPEM)
	if err != nil {
		return fmt.Errorf("failed to extract certificate serial: %w", err)
	}

	storageKey := operatorID
	if len(operatorID) > 8 {
		storageKey = operatorID[len(operatorID)-8:]
	}

	record := map[string]interface{}{
		"client_id":      operatorID,
		"tenant":         flagRegisterOperatorTenant,
		"public_key_pem": string(publicKeyPEM),
		"cert_serial":    certSerial,
		"registered_at":  time.Now().UTC(),
		"expires_at":     expiresAt.UTC(),
		"registered_by":  "nstance-admin",
	}

	recordData, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal registration record: %w", err)
	}

	recordKey := fmt.Sprintf("operator/%s.%s.json", flagRegisterOperatorTenant, storageKey)
	if err := clusterStorage.Put(ctx, recordKey, recordData); err != nil {
		return fmt.Errorf("failed to write registration record: %w", err)
	}

	outputDir := flagRegisterOperatorOutputDir
	if outputDir == "" {
		outputDir = filepath.Join(flagTempDir, "cli-operator-identity")
	}

	if err := os.MkdirAll(outputDir, 0700); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	if err := os.WriteFile(filepath.Join(outputDir, "ca.crt"), caCertPEM, 0600); err != nil {
		return fmt.Errorf("failed to write CA certificate: %w", err)
	}

	if privateKeyPEM != nil {
		if err := os.WriteFile(filepath.Join(outputDir, "identity.key"), privateKeyPEM, 0600); err != nil {
			return fmt.Errorf("failed to write private key: %w", err)
		}
	}

	if err := os.WriteFile(filepath.Join(outputDir, "identity.crt"), certPEM, 0600); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	fmt.Println("Operator registered successfully")
	fmt.Printf("  operator_id: %s\n", operatorID)
	fmt.Printf("  tenant: %s\n", flagRegisterOperatorTenant)
	fmt.Printf("  expires_at: %s\n", expiresAt.UTC().Format(time.RFC3339))
	fmt.Printf("  identity written to: %s/\n", outputDir)

	return nil
}

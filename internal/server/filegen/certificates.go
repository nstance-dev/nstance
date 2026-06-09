// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package filegen

import (
	"fmt"
	"strings"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

// prepareCertificateRequests builds certificate generation requests from required files
func (p *Generator) prepareCertificateRequests(
	cfg *config.Config,
	instance *localdb.Instance,
	template *config.TemplateConfig,
	filesRequired []string,
	processedFiles map[string]bool,
	templateData pki.CertificateTemplateData,
) ([]pki.CertificateRequest, error) {
	var requests []pki.CertificateRequest

	for _, filename := range filesRequired {
		// Check if this file is configured as a certificate in the template
		fileConfig, exists := template.Files[filename]
		if !exists || fileConfig.Kind != "certificate" {
			p.logger.Debug("File is not a certificate or not configured",
				"instance_id", instance.ID,
				"filename", filename)
			continue
		}

		// Mark this file as processed
		processedFiles[filename] = true

		// Get the public key for this certificate
		keyName, err := p.getKeyName(&fileConfig, filename)
		if err != nil {
			p.logger.Error("Failed to get key name for certificate",
				"instance_id", instance.ID,
				"filename", filename,
				"error", err)
			continue
		}
		publicKey, err := p.localDB.GetPublicKeyByFilename(instance.ID, keyName)
		if err != nil {
			p.logger.Error("Failed to get public key",
				"instance_id", instance.ID,
				"key_name", keyName,
				"error", err)
			continue
		}
		if publicKey == nil {
			p.logger.Debug("No public key found for certificate",
				"instance_id", instance.ID,
				"key_name", keyName)
			continue
		}

		// Get certificate template from configuration
		templateName, ok := fileConfig.Template.(string)
		if !ok {
			p.logger.Error("Certificate template must be a string",
				"instance_id", instance.ID,
				"filename", filename)
			continue
		}
		certConfig, exists := cfg.Certificates[templateName]
		if !exists {
			p.logger.Error("Certificate template not found",
				"instance_id", instance.ID,
				"template_name", templateName)
			continue
		}

		// Default CN to template name if not set
		cn := certConfig.CN
		if cn == nil {
			cn = &templateName
		}

		// Convert config.CertConfig to pki.CertificateConfig
		pkiCertConfig := &pki.CertificateConfig{
			Kind:         certConfig.Kind,
			CN:           cn,
			Organization: certConfig.Organization,
			DNS:          certConfig.DNS,
			IP:           certConfig.IP,
			URI:          certConfig.URI,
			TTL:          certConfig.TTL,
		}

		requests = append(requests, pki.CertificateRequest{
			InstanceID:        instance.ID,
			Tenant:            instance.Tenant,
			Filename:          filename,
			KeyName:           keyName,
			PublicKeyPEM:      []byte(publicKey.PublicKeyPEM),
			CertificateConfig: pkiCertConfig,
			TemplateData:      templateData,
		})
	}

	return requests, nil
}

// getKeyName returns the keypair base name for the explicitly configured public key file.
func (p *Generator) getKeyName(fileConfig *config.FileConfig, filename string) (string, error) {
	if fileConfig.Key == nil || fileConfig.Key.Name == "" {
		return "", fmt.Errorf("certificate file %s requires key.name", filename)
	}

	return strings.TrimSuffix(fileConfig.Key.Name, ".pub"), nil
}

// handleCertificateResults processes certificate generation results and returns generated files
func (p *Generator) handleCertificateResults(instanceID string, results []pki.CertificateResult) (map[string][]byte, error) {
	// Extract data for database update
	var filenames []string
	var serialNumbers []string
	generatedFiles := make(map[string][]byte)

	for _, result := range results {
		generatedFiles[result.Filename] = result.CertPEM

		filenames = append(filenames, result.KeyName)
		serialNumbers = append(serialNumbers, result.SerialNumber)
	}

	// Mark public keys as processed in database
	if err := p.localDB.MarkPublicKeysProcessed(instanceID, filenames, serialNumbers); err != nil {
		return nil, fmt.Errorf("failed to mark public keys as processed: %w", err)
	}

	return generatedFiles, nil
}

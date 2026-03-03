// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package filegen

import (
	"context"

	"github.com/nstance-dev/nstance/internal/server/config"
)

// generateSecrets handles secret file processing for required files
func (p *Generator) generateSecrets(ctx context.Context, instanceID string, cfg *config.Config, template *config.TemplateConfig, filesRequired []string, processedFiles map[string]bool) (map[string][]byte, error) {
	generatedFiles := make(map[string][]byte)
	var secretsProcessed int

	for _, filename := range filesRequired {
		// Check if this file is configured as a secret in the template
		fileConfig, exists := template.Files[filename]
		if !exists || fileConfig.Kind != "secret" {
			continue
		}

		// Mark this file as processed
		processedFiles[filename] = true

		// Validate secret source is specified
		if fileConfig.Source == "" {
			p.logger.Error("Secret file missing source configuration",
				"instance_id", instanceID,
				"filename", filename)
			continue
		}

		// Fetch secret content from store
		secretContent, err := p.secretsStore.Get(ctx, fileConfig.Source)
		if err != nil {
			p.logger.Error("Failed to fetch secret",
				"instance_id", instanceID,
				"filename", filename,
				"source", fileConfig.Source,
				"error", err)
			continue
		}

		// Add secret file to generated list
		generatedFiles[filename] = secretContent
		secretsProcessed++

		p.logger.Debug("Secret file generated",
			"instance_id", instanceID,
			"filename", filename,
			"source", fileConfig.Source)
	}

	if secretsProcessed > 0 {
		p.logger.Info("Secret files processed successfully",
			"instance_id", instanceID,
			"secrets_processed", secretsProcessed)
	}

	return generatedFiles, nil
}

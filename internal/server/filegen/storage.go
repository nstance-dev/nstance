// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package filegen

import (
	"context"
	"strings"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

// generateStorageFiles handles storage file processing for required files
func (p *Generator) generateStorageFiles(ctx context.Context, instanceID string, cfg *config.Config, template *config.TemplateConfig, filesRequired []string, processedFiles map[string]bool) (map[string][]byte, error) {
	generatedFiles := make(map[string][]byte)
	var storageProcessed int

	var templateData *pki.CertificateTemplateData

	for _, filename := range filesRequired {
		// Check if this file is configured as a storage file in the template
		fileConfig, exists := template.Files[filename]
		if !exists || fileConfig.Kind != "storage" {
			continue
		}

		// Mark this file as processed
		processedFiles[filename] = true

		// Validate storage source is specified
		if fileConfig.Source == "" {
			p.logger.Error("Storage file missing source configuration",
				"instance_id", instanceID,
				"filename", filename)
			continue
		}

		source := fileConfig.Source

		// Render source as a Go template if it contains template syntax
		if strings.Contains(source, "{{") {
			if templateData == nil {
				instance, err := p.localDB.GetInstance(instanceID)
				if err != nil {
					p.logger.Error("Failed to get instance for template rendering",
						"instance_id", instanceID,
						"filename", filename,
						"error", err)
					continue
				}
				td, err := p.buildTemplateData(ctx, cfg, instance)
				if err != nil {
					p.logger.Error("Failed to build template data for source rendering",
						"instance_id", instanceID,
						"filename", filename,
						"error", err)
					continue
				}
				templateData = &td
			}

			rendered, err := p.templateRenderer.processTemplate(source, *templateData)
			if err != nil {
				p.logger.Error("Failed to render storage source template",
					"instance_id", instanceID,
					"filename", filename,
					"source", source,
					"error", err)
				continue
			}
			source = rendered
		}

		// Fetch content from storage
		content, _, err := p.storageBackend.Get(ctx, source)
		if err != nil {
			p.logger.Error("Failed to fetch storage file",
				"instance_id", instanceID,
				"filename", filename,
				"source", source,
				"error", err)
			continue
		}

		// Add storage file to generated list
		generatedFiles[filename] = content
		storageProcessed++

		p.logger.Debug("Storage file generated",
			"instance_id", instanceID,
			"filename", filename,
			"source", source)
	}

	if storageProcessed > 0 {
		p.logger.Info("Storage files processed successfully",
			"instance_id", instanceID,
			"storage_processed", storageProcessed)
	}

	return generatedFiles, nil
}

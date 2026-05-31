// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package filegen

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/pki"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
)

// Generator handles the generation of instance files
type Generator struct {
	configLoader     *config.Loader
	localDB          *localdb.DB
	certService      *pki.BatchCertificateGenerator
	secretsStore     secrets.Store
	storageBackend   storage.Storage
	templateRenderer *TemplateRenderer
	logger           *slog.Logger
}

// NewGenerator creates a new file generator
func NewGenerator(
	configLoader *config.Loader,
	localDB *localdb.DB,
	certService *pki.BatchCertificateGenerator,
	secretsStore secrets.Store,
	storageBackend storage.Storage,
	logger *slog.Logger,
) *Generator {
	if logger == nil {
		logger = slog.Default()
	}

	return &Generator{
		configLoader:     configLoader,
		localDB:          localDB,
		certService:      certService,
		secretsStore:     secretsStore,
		storageBackend:   storageBackend,
		templateRenderer: NewTemplateRenderer(logger),
		logger:           logger,
	}
}

// GenerateFiles generates the specified files for an instance
func (p *Generator) GenerateFiles(ctx context.Context, instanceID string, files []string) (map[string][]byte, error) {
	if len(files) == 0 {
		return nil, nil
	}
	p.logger.Debug("Generating files", "instance_id", instanceID, "files", files)

	generatedFiles := make(map[string][]byte)

	// Get configuration
	cfg := p.configLoader.GetCurrent()
	if cfg == nil {
		return nil, fmt.Errorf("no configuration available")
	}

	// Get instance information
	instance, err := p.localDB.GetInstance(instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	// Derive template from instance group (merge static + dynamic groups)
	groups, err := config.GetGroups(ctx, p.configLoader, instance.Tenant)
	if err != nil {
		return nil, fmt.Errorf("failed to get groups: %w", err)
	}
	groupConfig, exists := groups[instance.Group]
	if !exists {
		return nil, fmt.Errorf("instance group %s not found", instance.Group)
	}
	templateName := groupConfig.Template

	// Get template config
	template, exists := cfg.Templates[templateName]
	if !exists {
		return nil, fmt.Errorf("template %s not found", templateName)
	}

	// Track which files have been processed by any handler
	processedFiles := make(map[string]bool)

	// Build certificate requests for missing certificate files
	certRequests, err := p.prepareCertificateRequests(cfg, instance, &template, files, processedFiles)
	if err != nil {
		return nil, fmt.Errorf("failed to build certificate requests: %w", err)
	}

	// Generate certificates in batch if any are needed
	if len(certRequests) > 0 {
		p.logger.Info("Generating certificates in batch",
			"instance_id", instanceID,
			"certificate_count", len(certRequests))

		results, err := p.certService.GenerateBatch(ctx, certRequests)
		if err != nil {
			return nil, fmt.Errorf("failed to generate certificate batch: %w", err)
		}

		// handleCertificateResults now returns the generated files
		certFiles, err := p.handleCertificateResults(instanceID, results)
		if err != nil {
			return nil, fmt.Errorf("failed to handle certificate results: %w", err)
		}
		for k, v := range certFiles {
			generatedFiles[k] = v
		}

		p.logger.Info("Certificate batch processed successfully",
			"instance_id", instanceID,
			"certificates_generated", len(results))
	}

	// Handle secret files
	secretFiles, err := p.generateSecrets(ctx, instanceID, cfg, &template, files, processedFiles)
	if err != nil {
		p.logger.Error("Failed to generate secret files", "instance_id", instanceID, "error", err)
		// Continue processing - don't fail the entire health report for secret errors
	} else {
		for k, v := range secretFiles {
			generatedFiles[k] = v
		}
	}

	// Handle storage files
	storageFiles, err := p.generateStorageFiles(ctx, instanceID, cfg, &template, files, processedFiles)
	if err != nil {
		p.logger.Error("Failed to generate storage files", "instance_id", instanceID, "error", err)
		// Continue processing - don't fail the entire health report for storage errors
	} else {
		for k, v := range storageFiles {
			generatedFiles[k] = v
		}
	}

	// Handle templated files
	templateFiles, err := p.generateTemplates(ctx, instanceID, cfg, &template, files, processedFiles)
	if err != nil {
		p.logger.Error("Failed to generate templated files", "instance_id", instanceID, "error", err)
		// Continue processing - don't fail the entire health report for template errors
	} else {
		for k, v := range templateFiles {
			generatedFiles[k] = v
		}
	}

	// Log warning for any files that weren't processed by any handler
	var unprocessedFiles []string
	for _, filename := range files {
		if !processedFiles[filename] {
			unprocessedFiles = append(unprocessedFiles, filename)
		}
	}
	if len(unprocessedFiles) > 0 {
		p.logger.Warn("Files requested but not configured for generation",
			"instance_id", instanceID,
			"files", unprocessedFiles,
			"hint", "Check that these files are configured in the instance template")
	}

	return generatedFiles, nil
}

// buildTemplateData creates template data for certificate generation
func (p *Generator) buildTemplateData(cfg *config.Config, instance *localdb.Instance) (pki.CertificateTemplateData, error) {
	// Get merged configuration for this instance
	tenantGroups := cfg.Groups[instance.Tenant]
	if tenantGroups == nil {
		return pki.CertificateTemplateData{}, fmt.Errorf("tenant %s not found in config", instance.Tenant)
	}
	group, exists := tenantGroups[instance.Group]
	if !exists {
		return pki.CertificateTemplateData{}, fmt.Errorf("group %s not found for tenant %s", instance.Group, instance.Tenant)
	}

	// Derive template from group
	templateName := group.Template

	mergedConfig, err := cfg.GetMergedConfig(templateName, group)
	if err != nil {
		return pki.CertificateTemplateData{}, fmt.Errorf("failed to get merged config: %w", err)
	}

	// Build template data
	templateData := pki.CreateCertificateTemplateData(
		instance.ID,
		mergedConfig.InstanceType,
		getStringValue(instance.Hostname),
		getStringValue(instance.FQDN),
		getStringValue(instance.IP4),
		getStringValue(instance.IP6),
		cfg.Cluster.ID,
		mergedConfig.Vars,
	)

	return templateData, nil
}

// getStringValue safely gets a string value from a pointer, returning empty string if nil
func getStringValue(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

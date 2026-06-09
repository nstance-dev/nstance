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
	caCertPEM        []byte
	imageGetter      ImageGetter
	secretsStore     secrets.Store
	storageBackend   storage.Storage
	templateRenderer *TemplateRenderer
	logger           *slog.Logger
}

// ImageGetter provides resolved image IDs for template rendering.
type ImageGetter interface {
	GetAll() map[string]string
}

// NewGenerator creates a new file generator
func NewGenerator(
	configLoader *config.Loader,
	localDB *localdb.DB,
	certService *pki.BatchCertificateGenerator,
	caCertPEM []byte,
	imageGetter ImageGetter,
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
		caCertPEM:        caCertPEM,
		imageGetter:      imageGetter,
		secretsStore:     secretsStore,
		storageBackend:   storageBackend,
		templateRenderer: NewTemplateRenderer(logger),
		logger:           logger,
	}
}

// GenerateFiles generates the specified files for an instance.
// If files is nil, it generates every file configured by the instance template.
func (p *Generator) GenerateFiles(ctx context.Context, instanceID string, files []string) (map[string][]byte, error) {
	if files != nil && len(files) == 0 {
		return nil, nil
	}

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

	// Get template config and build template data
	template, exists := cfg.Templates[templateName]
	if !exists {
		return nil, fmt.Errorf("template %s not found", templateName)
	}
	mergedConfig, err := cfg.GetMergedConfig(templateName, groupConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get merged config: %w", err)
	}
	templateData := p.buildTemplateData(cfg, instance, mergedConfig)
	if files == nil {
		files = make([]string, 0, len(template.Files))
		for filename := range template.Files {
			files = append(files, filename)
		}
	}
	p.logger.Debug("Generating files", "instance_id", instanceID, "files", files)

	// Track which files have been processed by any handler
	processedFiles := make(map[string]bool)

	// Build certificate requests for missing certificate files
	certRequests, err := p.prepareCertificateRequests(cfg, instance, &template, files, processedFiles, templateData)
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

		// Certificate issuance metadata is recorded by the PKI serial logger.
		for _, result := range results {
			generatedFiles[result.Filename] = result.CertPEM
		}

		p.logger.Info("Certificate batch processed successfully",
			"instance_id", instanceID,
			"certificates_generated", len(results))
	}

	// Handle secret files
	secretFiles, err := p.generateSecrets(ctx, instanceID, &template, files, processedFiles, templateData)
	if err != nil {
		p.logger.Error("Failed to generate secret files", "instance_id", instanceID, "error", err)
		// Continue processing - don't fail the entire health report for secret errors
	} else {
		for k, v := range secretFiles {
			generatedFiles[k] = v
		}
	}

	// Handle storage files
	storageFiles, err := p.generateStorageFiles(ctx, instanceID, &template, files, processedFiles, templateData)
	if err != nil {
		p.logger.Error("Failed to generate storage files", "instance_id", instanceID, "error", err)
		// Continue processing - don't fail the entire health report for storage errors
	} else {
		for k, v := range storageFiles {
			generatedFiles[k] = v
		}
	}

	// Handle templated files
	templateFiles, err := p.generateTemplates(instanceID, &template, files, processedFiles, templateData)
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

// buildTemplateData creates template data for file/certificate generation
func (p *Generator) buildTemplateData(cfg *config.Config, instance *localdb.Instance, mergedConfig *config.MergedConfig) pki.CertificateTemplateData {
	images := make(map[string]string)
	if p.imageGetter != nil {
		images = p.imageGetter.GetAll()
	}

	return pki.CertificateTemplateData{
		Instance: pki.InstanceData{
			ID:       instance.ID,
			Kind:     mergedConfig.Kind,
			Arch:     mergedConfig.Arch,
			Type:     mergedConfig.InstanceType,
			Hostname: getStringValue(instance.Hostname),
			FQDN:     getStringValue(instance.FQDN),
			IP4:      getStringValue(instance.IP4),
			IP6:      getStringValue(instance.IP6),
		},
		Cluster: pki.ClusterData{
			ID:     cfg.Cluster.ID,
			CACert: string(p.caCertPEM),
		},
		Server: pki.ServerData{
			Shard:            cfg.Shard.ID,
			RegistrationAddr: cfg.Shard.Advertise.RegistrationAddr,
			AgentAddr:        cfg.Shard.Advertise.AgentAddr,
			OperatorAddr:     cfg.Shard.Advertise.OperatorAddr,
		},
		Provider: pki.ProviderData{
			Kind:   cfg.Shard.Infra.Provider,
			Region: cfg.Shard.Infra.Region,
			Zone:   cfg.Shard.Infra.Zone,
		},
		Vars:  mergedConfig.Vars,
		Image: images,
	}
}

// getStringValue safely gets a string value from a pointer, returning empty string if nil
func getStringValue(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

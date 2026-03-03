// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package filegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"text/template"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

// generateTemplates handles templated file processing for required files
func (p *Generator) generateTemplates(ctx context.Context, instanceID string, cfg *config.Config, template *config.TemplateConfig, filesRequired []string, processedFiles map[string]bool) (map[string][]byte, error) {
	generatedFiles := make(map[string][]byte)
	var templatesProcessed int

	for _, filename := range filesRequired {
		// Check if this file is configured as a template in the template
		fileConfig, exists := template.Files[filename]
		if !exists {
			continue
		}

		// Only process template kinds
		if fileConfig.Kind != "env" && fileConfig.Kind != "json" && fileConfig.Kind != "string" {
			continue
		}

		// Mark this file as processed
		processedFiles[filename] = true

		// Get instance information
		instance, err := p.localDB.GetInstance(instanceID)
		if err != nil {
			p.logger.Error("Failed to get instance for template processing",
				"instance_id", instanceID,
				"filename", filename,
				"error", err)
			continue
		}

		// Build template data
		templateData, err := p.buildTemplateData(cfg, instance)
		if err != nil {
			p.logger.Error("Failed to build template data for templated file",
				"instance_id", instanceID,
				"filename", filename,
				"error", err)
			continue
		}

		// Process the templated file
		content, err := p.templateRenderer.Render(&fileConfig, templateData)
		if err != nil {
			p.logger.Error("Failed to render templated file",
				"instance_id", instanceID,
				"filename", filename,
				"kind", fileConfig.Kind,
				"error", err)
			continue
		}

		// Add templated file to generated list
		generatedFiles[filename] = content
		templatesProcessed++

		p.logger.Debug("Templated file generated",
			"instance_id", instanceID,
			"filename", filename,
			"kind", fileConfig.Kind,
			"size", len(content))
	}

	if templatesProcessed > 0 {
		p.logger.Info("Templated files processed successfully",
			"instance_id", instanceID,
			"templates_processed", templatesProcessed)
	}

	return generatedFiles, nil
}

// TemplateRenderer handles processing of templated files
type TemplateRenderer struct {
	logger *slog.Logger
}

// NewTemplateRenderer creates a new template processor
func NewTemplateRenderer(logger *slog.Logger) *TemplateRenderer {
	if logger == nil {
		logger = slog.Default()
	}

	return &TemplateRenderer{
		logger: logger,
	}
}

// Render processes a templated file based on its kind
func (tp *TemplateRenderer) Render(
	fileConfig *config.FileConfig,
	templateData pki.CertificateTemplateData,
) ([]byte, error) {
	switch fileConfig.Kind {
	case "env":
		return tp.processEnvTemplate(fileConfig, templateData)
	case "json":
		return tp.processJSONTemplate(fileConfig, templateData)
	case "string":
		return tp.processStringTemplate(fileConfig, templateData)
	default:
		return nil, fmt.Errorf("unsupported template kind: %s", fileConfig.Kind)
	}
}

// processEnvTemplate processes env template files (key=value format)
func (tp *TemplateRenderer) processEnvTemplate(
	fileConfig *config.FileConfig,
	templateData pki.CertificateTemplateData,
) ([]byte, error) {
	templateObj, ok := fileConfig.Template.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("env template must be an object")
	}

	var lines []string
	var keys []string

	// Sort keys for deterministic output
	for key := range templateObj {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := templateObj[key]
		valueStr, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("env template value for key %s must be a string", key)
		}

		// Process the value as a template
		processedValue, err := tp.processTemplate(valueStr, templateData)
		if err != nil {
			return nil, fmt.Errorf("failed to process template for env key %s: %w", key, err)
		}

		lines = append(lines, fmt.Sprintf("%s=%s", key, processedValue))
	}

	return []byte(strings.Join(lines, "\n")), nil
}

// processJSONTemplate processes JSON template files
func (tp *TemplateRenderer) processJSONTemplate(
	fileConfig *config.FileConfig,
	templateData pki.CertificateTemplateData,
) ([]byte, error) {
	templateObj, ok := fileConfig.Template.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("json template must be an object")
	}

	// Process the object recursively to handle templated string values
	processedObj, err := tp.processJSONObject(templateObj, templateData)
	if err != nil {
		return nil, fmt.Errorf("failed to process JSON template: %w", err)
	}

	// Marshal to JSON
	jsonBytes, err := json.MarshalIndent(processedObj, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return jsonBytes, nil
}

// processStringTemplate processes string template files
func (tp *TemplateRenderer) processStringTemplate(
	fileConfig *config.FileConfig,
	templateData pki.CertificateTemplateData,
) ([]byte, error) {
	templateStr, ok := fileConfig.Template.(string)
	if !ok {
		return nil, fmt.Errorf("string template must be a string")
	}

	processed, err := tp.processTemplate(templateStr, templateData)
	if err != nil {
		return nil, fmt.Errorf("failed to process string template: %w", err)
	}

	return []byte(processed), nil
}

// processJSONObject recursively processes a JSON object, templating string values
func (tp *TemplateRenderer) processJSONObject(obj interface{}, templateData pki.CertificateTemplateData) (interface{}, error) {
	switch v := obj.(type) {
	case string:
		// Process string as template
		return tp.processTemplate(v, templateData)
	case map[string]interface{}:
		// Process object recursively
		result := make(map[string]interface{})
		for key, value := range v {
			processedValue, err := tp.processJSONObject(value, templateData)
			if err != nil {
				return nil, fmt.Errorf("failed to process object key %s: %w", key, err)
			}
			result[key] = processedValue
		}
		return result, nil
	case []interface{}:
		// Process array recursively
		result := make([]interface{}, len(v))
		for i, item := range v {
			processedItem, err := tp.processJSONObject(item, templateData)
			if err != nil {
				return nil, fmt.Errorf("failed to process array item %d: %w", i, err)
			}
			result[i] = processedItem
		}
		return result, nil
	default:
		// Return non-string values as-is (numbers, booleans, null)
		return v, nil
	}
}

// processTemplate processes a string template using Go text/template
func (tp *TemplateRenderer) processTemplate(templateStr string, templateData pki.CertificateTemplateData) (string, error) {
	tmpl, err := template.New("file").Option("missingkey=zero").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

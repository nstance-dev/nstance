// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package instances

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"text/template"

	"github.com/nstance-dev/nstance/internal/server/config"
)

// UserdataTemplateData contains data available for userdata template processing
type UserdataTemplateData struct {
	Cluster  ClusterData       `json:"cluster"`
	Server   ServerData        `json:"server"`
	Provider ProviderData      `json:"provider"`
	Instance InstanceData      `json:"instance"`
	Vars     map[string]string `json:"vars"`
	Image    map[string]string `json:"image"` // Resolved image IDs (if any)
	Nonce    string            `json:"nonce"` // Registration nonce JWT
}

// ClusterData contains cluster information for template processing
type ClusterData struct {
	ID     string `json:"id"`
	CACert string `json:"ca_cert"` // CA certificate PEM
}

// ServerData contains server information for template processing
type ServerData struct {
	Shard            string `json:"shard"`
	RegistrationAddr string `json:"registration_addr"`
	AgentAddr        string `json:"agent_addr"`
	OperatorAddr     string `json:"operator_addr"`
}

// ProviderData contains provider information for template processing
type ProviderData struct {
	Kind   string `json:"kind"` // aws, azure, gcp
	Region string `json:"region"`
	Zone   string `json:"zone"`
}

// InstanceData contains instance information for template processing
type InstanceData struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Arch string `json:"arch"`
	Type string `json:"type"`
}

// processUserdataTemplate processes the userdata template for an instance
func (m *Manager) processUserdataTemplate(userdataConfig *config.UserdataConfig, templateData UserdataTemplateData) (string, error) {
	if userdataConfig == nil {
		return "", fmt.Errorf("no userdata template specified")
	}

	// Get template content based on source and encoding
	templateContent, err := m.getUserdataContent(userdataConfig)
	if err != nil {
		return "", fmt.Errorf("failed to get userdata template content: %w", err)
	}

	// Parse and execute template
	// missingkey=zero ensures missing map keys (e.g. .Vars.UNSET_KEY) render as ""
	// instead of the default "<no value>" which can break shell scripts.
	tmpl, err := template.New("userdata").Option("missingkey=zero").Parse(templateContent)
	if err != nil {
		return "", fmt.Errorf("failed to parse userdata template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData); err != nil {
		return "", fmt.Errorf("failed to execute userdata template: %w", err)
	}

	return buf.String(), nil
}

// getUserdataContent retrieves and decodes userdata content based on config
func (m *Manager) getUserdataContent(cfg *config.UserdataConfig) (string, error) {
	source := cfg.Source
	if source == "" {
		source = "inline"
	}

	encoding := cfg.Encoding
	if encoding == "" {
		encoding = "plain"
	}

	var rawContent []byte

	if source == "url" {
		// Download from URL
		resp, err := http.Get(cfg.Content)
		if err != nil {
			return "", fmt.Errorf("failed to download template: %w", err)
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		if resp.StatusCode != 200 {
			return "", fmt.Errorf("failed to download template: HTTP %d", resp.StatusCode)
		}

		rawContent, err = io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read template content: %w", err)
		}
	} else {
		// Inline content
		rawContent = []byte(cfg.Content)
	}

	// Decode based on encoding
	return m.decodeUserdata(rawContent, encoding)
}

// decodeUserdata decodes content based on encoding type
func (m *Manager) decodeUserdata(content []byte, encoding string) (string, error) {
	switch encoding {
	case "plain":
		return string(content), nil

	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(string(content))
		if err != nil {
			return "", fmt.Errorf("failed to decode base64: %w", err)
		}
		return string(decoded), nil

	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(content))
		if err != nil {
			return "", fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer func() {
			_ = reader.Close()
		}()

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			return "", fmt.Errorf("failed to decompress gzip: %w", err)
		}
		return string(decompressed), nil

	case "base64+gzip":
		// First decode base64
		decoded, err := base64.StdEncoding.DecodeString(string(content))
		if err != nil {
			return "", fmt.Errorf("failed to decode base64: %w", err)
		}
		// Then decompress gzip
		reader, err := gzip.NewReader(bytes.NewReader(decoded))
		if err != nil {
			return "", fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer func() {
			_ = reader.Close()
		}()

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			return "", fmt.Errorf("failed to decompress gzip: %w", err)
		}
		return string(decompressed), nil

	default:
		return "", fmt.Errorf("unsupported encoding: %s", encoding)
	}
}

// processArgs processes Args map by applying template interpolation to string values
func (m *Manager) processArgs(args map[string]interface{}, data UserdataTemplateData) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for key, value := range args {
		processed, err := m.processArgsValue(value, data)
		if err != nil {
			return nil, fmt.Errorf("failed to process args key %s: %w", key, err)
		}
		result[key] = processed
	}

	return result, nil
}

// processArgsValue recursively processes Args values, interpolating strings
func (m *Manager) processArgsValue(value interface{}, data UserdataTemplateData) (interface{}, error) {
	switch v := value.(type) {
	case string:
		// Process string as template
		tmpl, err := template.New("args").Option("missingkey=zero").Parse(v)
		if err != nil {
			return nil, fmt.Errorf("failed to parse args template: %w", err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("failed to execute args template: %w", err)
		}
		return buf.String(), nil
	case map[string]interface{}:
		// Recursively process nested map
		result := make(map[string]interface{})
		for k, val := range v {
			processed, err := m.processArgsValue(val, data)
			if err != nil {
				return nil, err
			}
			result[k] = processed
		}
		return result, nil
	case []interface{}:
		// Process array elements
		result := make([]interface{}, len(v))
		for i, val := range v {
			processed, err := m.processArgsValue(val, data)
			if err != nil {
				return nil, err
			}
			result[i] = processed
		}
		return result, nil
	default:
		// Return as-is for non-string, non-map, non-array values (numbers, bools, etc.)
		return value, nil
	}
}

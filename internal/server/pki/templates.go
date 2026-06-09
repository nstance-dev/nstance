// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package pki

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"text/template"
)

// CertificateConfig contains a certificate configuration with all templates processed
// SEE ALSO: CertConfig struct in internal/server/config/types.go
type CertificateConfig struct {
	Kind         string
	CN           *string
	Organization []string
	DNS          []string
	IP           []string
	URI          []string
	Country      []string
	Province     []string
	Locality     []string
	Street       []string
	PostalCode   []string
	TTL          int
}

// InstanceData contains all data available for instance template processing
type InstanceData struct {
	ID       string
	Kind     string
	Arch     string
	Type     string
	Hostname string
	FQDN     string
	IP4      string
	IP6      string
}

// ClusterData contains cluster data available for template processing.
type ClusterData struct {
	ID     string
	CACert string
}

// ServerData contains nstance-server addresses available for template processing.
type ServerData struct {
	Shard            string
	RegistrationAddr string
	AgentAddr        string
	OperatorAddr     string
}

// ProviderData contains provider data available for template processing.
type ProviderData struct {
	Kind   string
	Region string
	Zone   string
}

// CertificateTemplateData contains all data available for certificate template processing
type CertificateTemplateData struct {
	Instance InstanceData
	Cluster  ClusterData
	Server   ServerData
	Provider ProviderData
	Vars     map[string]string
	Image    map[string]string
}

// ProcessCertificateTemplate processes a certificate template with the given data
func ProcessCertificateTemplate(certConfig CertificateConfig, data CertificateTemplateData) (*CertificateConfig, error) {
	result := &CertificateConfig{
		Kind: certConfig.Kind,
		TTL:  certConfig.TTL,
	}

	// Set default TTL if not specified
	if result.TTL == 0 {
		result.TTL = 8760 // 1 year in hours
	}

	// Process CN template
	if certConfig.CN != nil {
		cn, err := processTemplate("CN", *certConfig.CN, data)
		if err != nil {
			return nil, fmt.Errorf("failed to process CN template: %w", err)
		}
		result.CN = &cn
	}

	// Process organization (no templating for organization fields)
	result.Organization = make([]string, len(certConfig.Organization))
	copy(result.Organization, certConfig.Organization)

	// Process DNS SANs
	result.DNS = make([]string, 0, len(certConfig.DNS))
	for _, dnsTemplate := range certConfig.DNS {
		dns, err := processTemplate("DNS", dnsTemplate, data)
		if err != nil {
			return nil, fmt.Errorf("failed to process DNS template: %w", err)
		}
		// Only add non-empty DNS names
		if strings.TrimSpace(dns) != "" {
			result.DNS = append(result.DNS, dns)
		}
	}

	// Process IP SANs
	result.IP = make([]string, 0, len(certConfig.IP))
	for _, ipTemplate := range certConfig.IP {
		ip, err := processTemplate("IP", ipTemplate, data)
		if err != nil {
			return nil, fmt.Errorf("failed to process IP template: %w", err)
		}
		// Validate and only add valid IP addresses
		if strings.TrimSpace(ip) != "" && net.ParseIP(ip) != nil {
			result.IP = append(result.IP, ip)
		}
	}

	// Process URI SANs
	result.URI = make([]string, 0, len(certConfig.URI))
	for _, uriTemplate := range certConfig.URI {
		uri, err := processTemplate("URI", uriTemplate, data)
		if err != nil {
			return nil, fmt.Errorf("failed to process URI template: %w", err)
		}
		if strings.TrimSpace(uri) != "" {
			result.URI = append(result.URI, uri)
		}
	}

	// Copy other fields (no templating for now, but could be added if needed)
	result.Country = make([]string, len(certConfig.Country))
	copy(result.Country, certConfig.Country)

	result.Province = make([]string, len(certConfig.Province))
	copy(result.Province, certConfig.Province)

	result.Locality = make([]string, len(certConfig.Locality))
	copy(result.Locality, certConfig.Locality)

	result.Street = make([]string, len(certConfig.Street))
	copy(result.Street, certConfig.Street)

	result.PostalCode = make([]string, len(certConfig.PostalCode))
	copy(result.PostalCode, certConfig.PostalCode)

	return result, nil
}

// processTemplate processes a single template string with the given data
func processTemplate(name, templateStr string, data CertificateTemplateData) (string, error) {
	// If there are no template markers, return as-is
	if !strings.Contains(templateStr, "{{") {
		return templateStr, nil
	}

	// Parse and execute template
	tmpl, err := template.New(name).Option("missingkey=zero").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// ProcessTemplateString processes a single template string with the given data - exposed for testing
func ProcessTemplateString(templateStr string, data CertificateTemplateData) (string, error) {
	return processTemplate("test", templateStr, data)
}

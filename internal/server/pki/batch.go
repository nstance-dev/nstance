// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package pki

import (
	"context"
	"fmt"
	"time"
)

// CertificateRequest represents a single certificate generation request
type CertificateRequest struct {
	InstanceID        string
	Tenant            string
	Filename          string
	PublicKeyPEM      []byte
	CertificateConfig *CertificateConfig
	TemplateData      CertificateTemplateData
}

// CertificateResult represents the result of certificate generation
type CertificateResult struct {
	Filename     string
	CertPEM      []byte
	ExpiresAt    time.Time
	SerialNumber string
}

// BatchCertificateGenerator handles batch certificate generation
type BatchCertificateGenerator struct {
	caCertPEM    []byte
	caKeyPEM     []byte
	serialLogger SerialLogger
}

// NewBatchCertificateGenerator creates a new batch certificate service
func NewBatchCertificateGenerator(caCertPEM, caKeyPEM []byte, serialLogger SerialLogger) *BatchCertificateGenerator {
	return &BatchCertificateGenerator{
		caCertPEM:    caCertPEM,
		caKeyPEM:     caKeyPEM,
		serialLogger: serialLogger,
	}
}

// GenerateBatch generates certificates in batch and logs serials atomically
func (s *BatchCertificateGenerator) GenerateBatch(ctx context.Context, requests []CertificateRequest) ([]CertificateResult, error) {
	if len(requests) == 0 {
		return nil, nil
	}

	results := make([]CertificateResult, 0, len(requests))
	logEntries := make([]SerialLogEntry, 0, len(requests))

	// Generate all certificates
	for _, req := range requests {
		// Process certificate template with instance data
		processedTemplate, err := ProcessCertificateTemplate(*req.CertificateConfig, req.TemplateData)
		if err != nil {
			return nil, fmt.Errorf("failed to process certificate template for %s: %w", req.Filename, err)
		}

		// Generate certificate
		certPEM, expiresAt, err := GenerateClientCertificateWithConfig(
			s.caCertPEM,
			s.caKeyPEM,
			req.PublicKeyPEM,
			req.InstanceID,
			processedTemplate,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to generate certificate for %s: %w", req.Filename, err)
		}

		// Generate unique serial for logging
		serial, err := GenerateSerialNumber()
		if err != nil {
			return nil, fmt.Errorf("failed to generate serial number for %s: %w", req.Filename, err)
		}

		results = append(results, CertificateResult{
			Filename:     req.Filename,
			CertPEM:      certPEM,
			ExpiresAt:    expiresAt,
			SerialNumber: serial,
		})

		logEntries = append(logEntries, SerialLogEntry{
			CertificateName: req.Filename,
			SerialNumber:    serial,
			ExpiresAt:       expiresAt,
		})
	}

	// Write all serials in a single atomic operation
	if len(logEntries) > 0 {
		instanceID := requests[0].InstanceID // All requests should be for same instance
		tenant := requests[0].Tenant
		if tenant == "" {
			return nil, fmt.Errorf("tenant is required for certificate batch")
		}
		if err := s.serialLogger.WriteBatch(ctx, tenant, instanceID, logEntries); err != nil {
			return nil, fmt.Errorf("failed to write certificate serial log: %w", err)
		}
	}

	return results, nil
}

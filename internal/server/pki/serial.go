// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package pki

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// SerialLogEntry represents a certificate issuance log entry
type SerialLogEntry struct {
	CertificateName string    `json:"certificate_name"`
	SerialNumber    string    `json:"serial_number"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// SerialLogger interface for writing certificate serial logs
type SerialLogger interface {
	WriteBatch(ctx context.Context, tenant, instanceID string, entries []SerialLogEntry) error
}

// S3SerialLogger implements SerialLogger using S3 storage
type S3SerialLogger struct {
	storage Storage
	shard   string
}

// Storage interface for S3 operations (matches existing storage abstraction)
type Storage interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, string, error)
}

// NewS3SerialLogger creates a new S3-based serial logger
func NewS3SerialLogger(storage Storage, shard string) SerialLogger {
	return &S3SerialLogger{
		storage: storage,
		shard:   shard,
	}
}

// WriteBatch writes certificate serial log entries to S3 in a single batch file
// S3 path format: certlog/{shard}/{tenant}.{timestamp}.{instanceID}.json
func (s *S3SerialLogger) WriteBatch(ctx context.Context, tenant, instanceID string, entries []SerialLogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	if tenant == "" {
		return fmt.Errorf("tenant is required")
	}

	// Create certificate log batch entry according to spec format
	logBatch := map[string]interface{}{
		"instance_id":  instanceID,
		"tenant":       tenant,
		"issued_at":    time.Now().UTC().Format(time.RFC3339),
		"batch_size":   len(entries),
		"certificates": entries,
	}

	// Serialize to JSON
	logData, err := json.Marshal(logBatch)
	if err != nil {
		return fmt.Errorf("failed to marshal certificate log: %w", err)
	}

	// Generate millisecond-precision timestamp for filename as per spec
	// Timestamp comes first for time-ordered listings, with instance ID for debugging
	// Format: {tenant}.{timestamp}.{instanceID}.json (within shard scope)
	timestamp := time.Now().UTC().Format("20060102150405000")
	logFilePath := fmt.Sprintf("certlog/%s.%s.%s.json", tenant, timestamp, instanceID)

	// Write to S3
	if err := s.storage.Put(ctx, logFilePath, logData); err != nil {
		return fmt.Errorf("failed to write certificate log to S3: %w", err)
	}

	return nil
}

// GenerateSerialNumber generates a unique serial number for certificate logging
func GenerateSerialNumber() (string, error) {
	// Generate 16 random bytes for a 128-bit serial number
	serialBytes := make([]byte, 16)
	if _, err := rand.Read(serialBytes); err != nil {
		return "", fmt.Errorf("failed to generate random serial: %w", err)
	}

	// Convert to hex string
	return hex.EncodeToString(serialBytes), nil
}

// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package receiver

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"regexp"
	"strings"
)

// ValidatorFunc validates a received file's contents.
type ValidatorFunc func(filename string, content []byte) error

// DefaultValidators defines the default validation rules keyed by file
// extension (lowercase).
var DefaultValidators = map[string]ValidatorFunc{
	".env":  validateEnvFile,
	".crt":  validatePEMCertificate,
	".pub":  validatePEMPublicKey,
	".key":  validatePEMPrivateKey,
	".json": validateJSONFile,
}

var validateEnvFileKeyFormat = regexp.MustCompile(`^[A-Z0-9_]+$`)

func validateEnvFile(filename string, envContent []byte) error {
	for _, line := range strings.Split(string(envContent), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			return fmt.Errorf("error: invalid env file format")
		}
		key := strings.SplitN(line, "=", 2)[0]
		if !validateEnvFileKeyFormat.MatchString(key) {
			return fmt.Errorf("error: invalid env file format")
		}
	}
	return nil
}

func validatePEMCertificate(filename string, pemContent []byte) error {
	block, _ := pem.Decode(pemContent)
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("error: invalid PEM certificate")
	}

	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return fmt.Errorf("error: invalid PEM certificate")
	}
	return nil
}

func validatePEMPublicKey(filename string, pemContent []byte) error {
	block, _ := pem.Decode(pemContent)
	if block == nil || block.Type != "PUBLIC KEY" {
		return fmt.Errorf("error: invalid PEM public key")
	}
	if _, err := x509.ParsePKIXPublicKey(block.Bytes); err != nil {
		return fmt.Errorf("error: invalid PEM public key")
	}
	return nil
}

func validatePEMPrivateKey(filename string, pemContent []byte) error {
	block, _ := pem.Decode(pemContent)
	if block == nil || block.Type != "PRIVATE KEY" {
		return fmt.Errorf("error: invalid PEM key")
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		return fmt.Errorf("error: invalid PEM key")
	}
	return nil
}

func validateJSONFile(filename string, jsonContent []byte) error {
	var data interface{}
	if err := json.Unmarshal(jsonContent, &data); err != nil {
		return fmt.Errorf("error: invalid JSON file format")
	}
	return nil
}

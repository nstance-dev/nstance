// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/tailscale/hujson"
)

// ValidateFile validates a local configuration file
func ValidateFile(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file '%s': %w", filePath, err)
	}
	return ParseBytes(data)
}

// ParseBytes parses and validates configuration from bytes
func ParseBytes(data []byte) (*Config, error) {
	// Convert JSONC to standard JSON
	jsonData, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse configuration JSONC: %w", err)
	}

	// Parse into Config struct
	var config Config
	if err := json.Unmarshal(jsonData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse configuration JSONC: %w", err)
	}

	// Set defaults
	config.SetDefaults()

	// Validate
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validation errors: %w", err)
	}

	return &config, nil
}

// RegisterCustomValidators registers custom validation functions
func RegisterCustomValidators(v *validator.Validate) error {
	validators := map[string]validator.Func{
		"url_or_file": validateURLOrFile,
		"s3_bucket":   validateS3Bucket,
		"file_path":   validateFilePath,
		"addr":        validateAddr,
	}

	for tag, fn := range validators {
		if err := v.RegisterValidation(tag, fn); err != nil {
			return fmt.Errorf("failed to register validator %s: %w", tag, err)
		}
	}

	return nil
}

// validateURLOrFile validates that a field is either a valid URL or a file path
func validateURLOrFile(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true // Allow empty for optional fields
	}

	// Check if it's a URL
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		_, err := url.Parse(value)
		return err == nil
	}

	// If it looks like it might be a URL but with wrong scheme, reject it
	if strings.Contains(value, "://") {
		return false
	}

	// Check if it's a file path (absolute or relative)
	// For validation purposes, we just check if it's a reasonable path format
	cleaned := filepath.Clean(value)
	return cleaned != "." && cleaned != ".."
}

// validateS3Bucket validates S3 bucket name format
func validateS3Bucket(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return false // Bucket name is required
	}

	// S3 bucket naming rules (simplified):
	// - 3-63 characters
	// - lowercase letters, numbers, hyphens
	// - cannot start or end with hyphen
	// - cannot contain consecutive hyphens
	if len(value) < 3 || len(value) > 63 {
		return false
	}

	if strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return false
	}

	if strings.Contains(value, "--") {
		return false
	}

	pattern := `^[a-z0-9-]+$`
	matched, err := regexp.MatchString(pattern, value)
	return err == nil && matched
}

// validateFilePath validates that a file path exists on disk
func validateFilePath(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true // Allow empty for optional fields
	}

	// Check if file exists
	_, err := os.Stat(value)
	return err == nil
}

// validateAddr validates that a field is in the format "IP:port" or "hostname:port"
func validateAddr(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return false // address:port is required
	}

	host, portStr, err := net.SplitHostPort(value)
	if err != nil {
		return false
	}

	// Validate host part - allow empty (auto-detected later), IP addresses, or hostnames
	if host != "" {
		if ip := net.ParseIP(host); ip == nil {
			// If not a valid IP, check if it's a valid hostname
			if len(host) > 253 {
				return false
			}
			// Basic hostname validation (more permissive than FQDN)
			if strings.Contains(host, "..") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
				return false
			}
		}
	}

	// Validate port part
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return false
	}

	return true
}

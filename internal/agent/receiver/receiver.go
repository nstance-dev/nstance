// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package receiver

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const lastCompletedTimestampFile = "write-files.last"

// Receiver processes streamed files and writes them to disk.
type Receiver struct {
	logger     *slog.Logger
	config     ReceiverConfig
	validators map[string]ValidatorFunc
}

// ReceiverConfig holds runtime options for the file receiver.
type ReceiverConfig struct {
	RecvDir     string
	IdentityDir string      // Path to identity directory for writing config.hash
	FileMode    os.FileMode // permissions used when writing files; default 0640.
}

func NewReceiver(logger *slog.Logger, cfg ReceiverConfig) *Receiver {
	if cfg.FileMode == 0 {
		cfg.FileMode = 0o640
	}
	return &Receiver{
		logger:     logger,
		config:     cfg,
		validators: DefaultValidators,
	}
}

// ReceiveFiles processes a map of filenames to file data,
// validates the file contents based on the filename extension (e.g. `.crt`),
// and writes each file to the receive directory.
func (r *Receiver) ReceiveFiles(payload map[string][]byte) error {
	if len(payload) == 0 {
		return nil
	}

	for fileName := range payload {
		if strings.TrimSpace(fileName) == "" {
			return fmt.Errorf("empty filename in payload")
		}
		if filepath.IsAbs(fileName) || filepath.Base(fileName) != fileName {
			return fmt.Errorf("invalid filename: %s", fileName)
		}
	}

	recvDir := r.config.RecvDir
	var err error
	writtenCount := 0
	for fileName, fileContent := range payload {
		filePath := filepath.Join(recvDir, fileName)
		errorFilePath := filepath.Join(recvDir, "errors", fileName)
		validator := r.validators[strings.ToLower(filepath.Ext(fileName))]
		if validator == nil {
			return fmt.Errorf("unsupported file extension %s", filepath.Ext(fileName))
		}
		err = validator(fileName, fileContent)
		if err != nil {
			// Validation failures are surfaced via health reporting; we log and write the error file
			// but return nil so callers continue streaming without immediate failure.
			wrappedErr := fmt.Errorf("validation failed for %s: %w", fileName, err)
			r.logger.Error(wrappedErr.Error(), "filename", fileName)
			// Ensure errors directory exists
			if err := os.MkdirAll(filepath.Join(recvDir, "errors"), 0755); err != nil {
				r.logger.Error("failed to create errors directory", "err", err)
				continue
			}
			if err := os.WriteFile(errorFilePath, []byte(err.Error()), r.config.FileMode); err != nil {
				r.logger.Error("failed to write error file", "filename", filepath.Join("errors", fileName), "err", err)
			}
			continue
		}
		_ = os.Remove(errorFilePath)
		if err := os.WriteFile(filePath, fileContent, r.config.FileMode); err != nil {
			r.logger.Error("failed to write received file", "filename", fileName, "err", err)
			return fmt.Errorf("failed to write received file %s: %w", fileName, err)
		}
		writtenCount++
	}

	if writtenCount == 0 {
		return nil
	}

	lastCompleteTsFile := filepath.Join(recvDir, lastCompletedTimestampFile)
	nowTs := fmt.Sprintf("%d", time.Now().UTC().UnixMilli())
	if err := os.WriteFile(lastCompleteTsFile, []byte(nowTs), r.config.FileMode); err != nil {
		r.logger.Error("failed to write last completed timestamp file", "err", err, "filename", lastCompletedTimestampFile)
		return nil
	}
	r.logger.Info("successfully received files", "count", writtenCount, "last_completed_timestamp", nowTs)

	return nil
}

// ReceiveConfigHash updates the config.hash file in the identity directory
func (r *Receiver) ReceiveConfigHash(configHash string) error {
	if r.config.IdentityDir == "" {
		r.logger.Warn("Identity directory not configured, cannot update config hash")
		return nil
	}

	configHashPath := filepath.Join(r.config.IdentityDir, "config.hash")
	if err := os.WriteFile(configHashPath, []byte(configHash), r.config.FileMode); err != nil {
		return fmt.Errorf("failed to write config hash: %w", err)
	}

	r.logger.Info("Updated config hash", "hash", configHash)
	return nil
}

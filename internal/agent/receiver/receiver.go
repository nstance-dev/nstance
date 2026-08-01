// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
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

// ReceiveFiles validates and applies a file patch, then publishes its hash and completion marker.
func (r *Receiver) ReceiveFiles(payload map[string][]byte, configHash string) error {
	if configHash == "" {
		return fmt.Errorf("config hash is required")
	}
	if r.config.IdentityDir == "" {
		return fmt.Errorf("identity directory not configured")
	}
	for fileName := range payload {
		if strings.TrimSpace(fileName) == "" {
			return fmt.Errorf("empty filename in payload")
		}
		if filepath.IsAbs(fileName) || filepath.Base(fileName) != fileName || strings.HasPrefix(fileName, ".") || fileName == lastCompletedTimestampFile {
			return fmt.Errorf("invalid filename: %s", fileName)
		}
	}
	rejected := false
	for fileName, fileContent := range payload {
		validator := r.validators[strings.ToLower(filepath.Ext(fileName))]
		var validationErr error
		if validator == nil {
			validationErr = fmt.Errorf("unsupported file extension %s", filepath.Ext(fileName))
		} else if err := validator(fileName, fileContent); err != nil {
			validationErr = fmt.Errorf("validation failed for %s: %w", fileName, err)
		}
		if validationErr != nil {
			r.logger.Error(validationErr.Error(), "filename", fileName)
			// Health reports read validation failures from the errors directory.
			errorsDir := filepath.Join(r.config.RecvDir, "errors")
			if mkdirErr := os.MkdirAll(errorsDir, 0o755); mkdirErr != nil {
				return fmt.Errorf("%w; failed to create errors directory: %v", validationErr, mkdirErr)
			}
			if writeErr := os.WriteFile(filepath.Join(errorsDir, fileName), []byte(validationErr.Error()), r.config.FileMode); writeErr != nil {
				return fmt.Errorf("%w; failed to write error file: %v", validationErr, writeErr)
			}
			// Reject the patch without stopping the stream. The next health report
			// includes the validation error so the server can retry the file.
			rejected = true
		}
	}
	if rejected {
		return nil
	}

	for fileName, fileContent := range payload {
		if err := replaceFile(filepath.Join(r.config.RecvDir, fileName), fileContent, r.config.FileMode); err != nil {
			return fmt.Errorf("failed to replace received file %s: %w", fileName, err)
		}
	}
	for fileName := range payload {
		errorPath := filepath.Join(r.config.RecvDir, "errors", fileName)
		if err := os.Remove(errorPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove validation error for %s: %w", fileName, err)
		}
	}
	if err := replaceFile(filepath.Join(r.config.IdentityDir, "config.hash"), []byte(configHash), r.config.FileMode); err != nil {
		return fmt.Errorf("failed to replace config hash: %w", err)
	}
	nowTs := fmt.Sprintf("%d", time.Now().UTC().UnixMilli())
	if err := replaceFile(filepath.Join(r.config.RecvDir, lastCompletedTimestampFile), []byte(nowTs), r.config.FileMode); err != nil {
		return fmt.Errorf("failed to publish file transfer: %w", err)
	}
	r.logger.Info("successfully applied file patch", "updated", len(payload), "last_completed_timestamp", nowTs)
	return nil
}

// replaceFile writes a temporary file beside path and renames it into place.
func replaceFile(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".nstance-recv-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	failed := true
	defer func() {
		if failed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	failed = false
	return nil
}

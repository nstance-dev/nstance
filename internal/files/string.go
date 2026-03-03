// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package files

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// LoadString reads a text file and returns its contents as a trimmed string.
// Returns empty string if the file does not exist and required is false.
func LoadString(logger *slog.Logger, path string, file string, required bool) (string, error) {
	if path == "" {
		return "", fmt.Errorf("file directory path is invalid: %s", path)
	}
	if file == "" {
		return "", fmt.Errorf("filename is invalid: %s", file)
	}
	filePath := filepath.Join(path, file)

	fh, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			if required {
				return "", fmt.Errorf("file does not exist: %s", filePath)
			}
			return "", nil
		}
		return "", fmt.Errorf("error opening file %s: %s", filePath, err)
	}
	defer func() {
		_ = fh.Close()
	}()

	stringData, err := io.ReadAll(fh)
	if err != nil {
		return "", fmt.Errorf("error reading file %s: %s", filePath, err)
	}
	return strings.TrimSpace(string(stringData)), nil
}

// WriteString writes a trimmed string to a file.
func WriteString(path string, file string, data string, mode os.FileMode) error {
	if path == "" {
		return fmt.Errorf("file directory path is invalid")
	}
	if file == "" {
		return fmt.Errorf("filename is invalid")
	}
	if mode == 0 {
		return fmt.Errorf("file mode %d is invalid", mode)
	}
	filePath := filepath.Join(path, file)

	data = strings.TrimSpace(data)

	fh, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("error creating file: %s", err)
	}
	defer func() {
		_ = fh.Close()
	}()

	_, err = fh.WriteString(data)
	if err != nil {
		return fmt.Errorf("error writing to file: %s", err)
	}

	return nil
}

// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package files

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// LoadJWT reads a JWT token from a file (first line only, trimmed).
// Returns nil if the file does not exist and required is false.
func LoadJWT(logger *slog.Logger, path string, file string, required bool) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("file directory path is invalid: %s", path)
	}
	if file == "" {
		return nil, fmt.Errorf("filename is invalid: %s", file)
	}
	filePath := filepath.Join(path, file)

	fh, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			if required {
				return nil, fmt.Errorf("file does not exist: %s", filePath)
			}
			return nil, nil
		}
		return nil, fmt.Errorf("error opening file %s: %s", filePath, err)
	}
	defer func() {
		_ = fh.Close()
	}()

	scanner := bufio.NewScanner(fh)
	if scanner.Scan() {
		return bytes.TrimSpace(scanner.Bytes()), nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file %s: %s", filePath, err)
	}
	return nil, fmt.Errorf("file is empty: %s", filePath)
}

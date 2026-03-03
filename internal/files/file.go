// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package files

import (
	"fmt"
	"os"
	"path/filepath"
)

func DeleteFile(path string, file string) error {
	if path == "" {
		return fmt.Errorf("file directory path is invalid: %s", path)
	}
	if file == "" {
		return fmt.Errorf("filename is invalid: %s", file)
	}
	filePath := filepath.Join(path, file)

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if fileInfo.IsDir() {
		return fmt.Errorf("cannot delete '%s': is a directory", path)
	}

	return os.Remove(filePath)
}

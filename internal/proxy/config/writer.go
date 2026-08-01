// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nstance-dev/nstance/pkg/proxy"
)

// Writer atomically replaces the root-owned proxy runtime configuration.
type Writer struct {
	Path string
	UID  int
	GID  int
	Mode os.FileMode
}

// Write atomically persists a static proxy configuration in the target directory.
func (w Writer) Write(config proxy.Config) error {
	if w.Path == "" {
		return fmt.Errorf("proxy config path is required")
	}
	mode := w.Mode
	if mode == 0 {
		mode = 0640
	}
	directory := filepath.Dir(w.Path)
	if err := PrepareRuntimeDirectory(directory, w.UID, w.GID); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".nstance-proxy-*")
	if err != nil {
		return fmt.Errorf("create temporary proxy config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode proxy config: %w", err)
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set proxy config mode: %w", err)
	}
	if err := temporary.Chown(w.UID, w.GID); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set proxy config ownership: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync proxy config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close proxy config: %w", err)
	}
	if err := os.Rename(temporaryPath, w.Path); err != nil {
		return fmt.Errorf("replace proxy config: %w", err)
	}
	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open proxy runtime directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync proxy runtime directory: %w", err)
	}
	return nil
}

// PrepareRuntimeDirectory creates a non-symlinked root-owned directory traversable by the proxy group.
func PrepareRuntimeDirectory(directory string, uid, gid int) error {
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(directory, 0750); err != nil {
			return fmt.Errorf("create proxy runtime directory: %w", err)
		}
		info, err = os.Lstat(directory)
	}
	if err != nil {
		return fmt.Errorf("inspect proxy runtime directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("proxy runtime path %s is not a real directory", directory)
	}
	if err := os.Chown(directory, uid, gid); err != nil {
		return fmt.Errorf("set proxy runtime directory ownership: %w", err)
	}
	if err := os.Chmod(directory, 0750); err != nil {
		return fmt.Errorf("set proxy runtime directory mode: %w", err)
	}
	return nil
}

// ValidateRuntimePath restricts a generated runtime file to a direct child of runtimeDirectory.
func ValidateRuntimePath(path, runtimeDirectory string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("runtime path %q must be absolute and clean", path)
	}
	runtimeDirectory = filepath.Clean(runtimeDirectory)
	if filepath.Dir(path) != runtimeDirectory || filepath.Base(path) == "." {
		return fmt.Errorf("runtime path %q must be a direct child of %s", path, runtimeDirectory)
	}

	current := string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(runtimeDirectory, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return fmt.Errorf("inspect runtime path component %s: %w", current, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("runtime path component %s is not a real directory", current)
		}
	}
	return nil
}

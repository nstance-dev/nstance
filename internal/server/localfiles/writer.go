// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

// Package localfiles atomically publishes files for the local vmconfig receive watcher.
package localfiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

const (
	completionFile = "write-files.last"
	manifestFile   = ".nstance-local-files.json"
)

// Writer publishes a batch of root-owned local runtime files.
type Writer struct {
	Directory string
	Mode      os.FileMode
}

// Write atomically replaces every file, then publishes a completion timestamp.
func (w Writer) Write(files map[string][]byte) error {
	if w.Directory == "" {
		return fmt.Errorf("local receive directory is required")
	}
	if err := os.MkdirAll(w.Directory, 0700); err != nil {
		return fmt.Errorf("create local receive directory: %w", err)
	}
	info, err := os.Lstat(w.Directory)
	if err != nil {
		return fmt.Errorf("inspect local receive directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("local receive path %s is not a real directory", w.Directory)
	}
	if err := os.Chmod(w.Directory, 0700); err != nil {
		return fmt.Errorf("secure local receive directory: %w", err)
	}
	mode := w.Mode
	if mode == 0 {
		mode = 0600
	}
	for name := range files {
		if name == "" || filepath.IsAbs(name) || filepath.Base(name) != name || name == completionFile || name == manifestFile {
			return fmt.Errorf("invalid local receive filename %q", name)
		}
	}
	previous, err := readManifest(filepath.Join(w.Directory, manifestFile))
	if err != nil {
		return err
	}
	for name, content := range files {
		if err := writeAtomic(w.Directory, name, content, mode); err != nil {
			return err
		}
	}
	for _, name := range previous {
		if _, exists := files[name]; !exists {
			if err := os.Remove(filepath.Join(w.Directory, name)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove stale local file %s: %w", name, err)
			}
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	manifest, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("encode local file manifest: %w", err)
	}
	if err := writeAtomic(w.Directory, manifestFile, manifest, mode); err != nil {
		return err
	}
	directory, err := os.Open(w.Directory)
	if err != nil {
		return fmt.Errorf("open local receive directory: %w", err)
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync local receive directory: %w", err)
	}
	completed := []byte(strconv.FormatInt(time.Now().UTC().UnixMilli(), 10))
	if err := writeAtomic(w.Directory, completionFile, completed, mode); err != nil {
		return err
	}
	return nil
}

// readManifest reads and validates the names owned by the previous publication.
func readManifest(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read local file manifest: %w", err)
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("decode local file manifest: %w", err)
	}
	for _, name := range names {
		if name == "" || filepath.IsAbs(name) || filepath.Base(name) != name || name == completionFile || name == manifestFile {
			return nil, fmt.Errorf("invalid filename %q in local file manifest", name)
		}
	}
	return names, nil
}

// writeAtomic publishes one file by renaming a synced temporary file.
func writeAtomic(directory, name string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(directory, ".nstance-local-tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary local file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set local file %s mode: %w", name, err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write local file %s: %w", name, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync local file %s: %w", name, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close local file %s: %w", name, err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, name)); err != nil {
		return fmt.Errorf("publish local file %s: %w", name, err)
	}
	return nil
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package localfiles

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWriterPublishesProtectedBatch verifies file permissions and replacement cleanup.
func TestWriterPublishesProtectedBatch(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "recv")
	writer := Writer{Directory: directory}
	if err := writer.Write(map[string][]byte{"tunnel.json": []byte(`{"token":"secret"}`)}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tunnel.json", completionFile} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
	}
	content, err := os.ReadFile(filepath.Join(directory, "tunnel.json"))
	if err != nil || string(content) != `{"token":"secret"}` {
		t.Fatalf("content = %q, error %v", content, err)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".nstance-local-tmp-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files = %v, error %v", matches, err)
	}
}

// TestWriterRejectsNestedFilename verifies files cannot escape the target directory.
func TestWriterRejectsNestedFilename(t *testing.T) {
	err := (Writer{Directory: t.TempDir()}).Write(map[string][]byte{"../secret": []byte("no")})
	if err == nil {
		t.Fatal("nested filename accepted")
	}
}

// TestWriterRemovesOnlyPreviouslyOwnedFiles verifies unrelated files are preserved.
func TestWriterRemovesOnlyPreviouslyOwnedFiles(t *testing.T) {
	directory := t.TempDir()
	writer := Writer{Directory: directory}
	if err := os.WriteFile(filepath.Join(directory, "unrelated"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(map[string][]byte{"old": []byte("old"), "keep": []byte("one")}); err != nil {
		t.Fatal(err)
	}
	firstCompletion, err := os.ReadFile(filepath.Join(directory, completionFile))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := writer.Write(map[string][]byte{"keep": []byte("two")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "old")); !os.IsNotExist(err) {
		t.Fatalf("removed file still exists: %v", err)
	}
	for _, name := range []string{"keep", "unrelated"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
	if err := writer.Write(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "keep")); !os.IsNotExist(err) {
		t.Fatalf("remove-all retained owned file: %v", err)
	}
	secondCompletion, err := os.ReadFile(filepath.Join(directory, completionFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(firstCompletion) == string(secondCompletion) {
		t.Fatal("empty update did not publish completion")
	}
}

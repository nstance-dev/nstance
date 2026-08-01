// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package receiver

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestReceiveFilesPublishesHashBeforeCompletion verifies file publication order.
func TestReceiveFilesPublishesHashBeforeCompletion(t *testing.T) {
	t.Parallel()

	recvDir := t.TempDir()
	identityDir := t.TempDir()
	receiver := NewReceiver(slog.New(slog.NewTextHandler(io.Discard, nil)), ReceiverConfig{
		RecvDir:     recvDir,
		IdentityDir: identityDir,
	})
	if err := receiver.ReceiveFiles(map[string][]byte{
		"config.json": []byte(`{"ok":true}`),
		"empty.env":   []byte{},
	}, "sha256:new"); err != nil {
		t.Fatalf("ReceiveFiles: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(recvDir, "config.json"):     `{"ok":true}`,
		filepath.Join(recvDir, "empty.env"):       "",
		filepath.Join(identityDir, "config.hash"): "sha256:new",
	} {
		content, err := os.ReadFile(path)
		if err != nil || string(content) != want {
			t.Fatalf("ReadFile(%s) = %q, %v; want %q", path, content, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(recvDir, lastCompletedTimestampFile)); err != nil {
		t.Fatalf("completion marker missing: %v", err)
	}
}

// TestReceiveFilesValidationFailureDoesNotMutate verifies invalid input leaves prior state intact.
func TestReceiveFilesValidationFailureDoesNotMutate(t *testing.T) {
	t.Parallel()

	recvDir := t.TempDir()
	identityDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(identityDir, "config.hash"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	receiver := NewReceiver(slog.New(slog.NewTextHandler(io.Discard, nil)), ReceiverConfig{
		RecvDir:     recvDir,
		IdentityDir: identityDir,
	})
	if err := receiver.ReceiveFiles(map[string][]byte{
		"good.json": []byte(`{"ok":true}`),
		"bad.json":  []byte(`{`),
	}, "new"); err != nil {
		t.Fatalf("ReceiveFiles stopped the stream after rejecting invalid content: %v", err)
	}
	if _, err := os.Stat(filepath.Join(recvDir, "good.json")); !os.IsNotExist(err) {
		t.Fatalf("good.json was published before validation completed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(recvDir, lastCompletedTimestampFile)); !os.IsNotExist(err) {
		t.Fatalf("completion marker was published: %v", err)
	}
	errorData, err := os.ReadFile(filepath.Join(recvDir, "errors", "bad.json"))
	if err != nil {
		t.Fatalf("validation error was not persisted for health reporting: %v", err)
	}
	if len(errorData) == 0 {
		t.Fatal("persisted validation error is empty")
	}
	hash, err := os.ReadFile(filepath.Join(identityDir, "config.hash"))
	if err != nil || string(hash) != "old" {
		t.Fatalf("config hash = %q, %v; want old", hash, err)
	}

	if err := receiver.ReceiveFiles(map[string][]byte{
		"good.json": []byte(`{"ok":true}`),
		"bad.json":  []byte(`{"fixed":true}`),
	}, "new"); err != nil {
		t.Fatalf("ReceiveFiles with corrected content: %v", err)
	}
	if _, err := os.Stat(filepath.Join(recvDir, "errors", "bad.json")); !os.IsNotExist(err) {
		t.Fatalf("validation error was not cleared after a valid transfer: %v", err)
	}
}

// TestReceiveFilesAppliesHashOnlyPatch verifies a patch need not contain files.
func TestReceiveFilesAppliesHashOnlyPatch(t *testing.T) {
	t.Parallel()
	recvDir := t.TempDir()
	identityDir := t.TempDir()
	receiver := NewReceiver(slog.New(slog.NewTextHandler(io.Discard, nil)), ReceiverConfig{
		RecvDir:     recvDir,
		IdentityDir: identityDir,
	})
	if err := receiver.ReceiveFiles(nil, "hash"); err != nil {
		t.Fatalf("ReceiveFiles: %v", err)
	}
	if hash, err := os.ReadFile(filepath.Join(identityDir, "config.hash")); err != nil || string(hash) != "hash" {
		t.Fatalf("config hash = %q, %v; want hash", hash, err)
	}
	if _, err := os.Stat(filepath.Join(recvDir, lastCompletedTimestampFile)); err != nil {
		t.Fatalf("completion marker missing: %v", err)
	}
}

// TestReceiveFilesLeavesOmittedFiles verifies patch semantics.
func TestReceiveFilesLeavesOmittedFiles(t *testing.T) {
	t.Parallel()
	recvDir := t.TempDir()
	receiver := NewReceiver(slog.New(slog.NewTextHandler(io.Discard, nil)), ReceiverConfig{
		RecvDir:     recvDir,
		IdentityDir: t.TempDir(),
	})
	if err := receiver.ReceiveFiles(map[string][]byte{
		"keep.env":   []byte("VALUE=first\n"),
		"remove.env": []byte("VALUE=obsolete\n"),
	}, "first"); err != nil {
		t.Fatal(err)
	}
	if err := receiver.ReceiveFiles(map[string][]byte{"keep.env": []byte("VALUE=second\n")}, "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(recvDir, "remove.env")); err != nil {
		t.Fatalf("omitted file was removed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(recvDir, "keep.env"))
	if err != nil || string(content) != "VALUE=second\n" {
		t.Fatalf("retained file = %q, %v", content, err)
	}
}

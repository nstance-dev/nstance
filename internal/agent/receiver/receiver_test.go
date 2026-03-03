// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package receiver

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestReceiverContinuesAfterValidationFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	receiver := NewReceiver(logger, ReceiverConfig{
		RecvDir: dir,
	})

	payload := map[string][]byte{
		"bad.crt":   []byte("-----BEGIN CERTIFICATE-----\nINVALID\n-----END CERTIFICATE-----"),
		"good.json": []byte(`{"ok":true}`),
	}

	if err := receiver.ReceiveFiles(payload); err != nil {
		t.Fatalf("ReceiveFiles: %v", err)
	}

	goodPath := filepath.Join(dir, "good.json")
	data, err := os.ReadFile(goodPath)
	if err != nil {
		t.Fatalf("ReadFile good.json: %v", err)
	}
	if string(data) != `{"ok":true}` {
		t.Fatalf("good.json contents = %q, want %q", data, `{"ok":true}`)
	}

	info, err := os.Stat(goodPath)
	if err != nil {
		t.Fatalf("Stat good.json: %v", err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("good.json perms = %o, want 640", info.Mode().Perm())
	}

	if _, err := os.Stat(filepath.Join(dir, "bad.crt")); !os.IsNotExist(err) {
		t.Fatalf("bad.crt should not exist, err = %v", err)
	}

	errorInfo, err := os.Stat(filepath.Join(dir, "errors", "bad.crt"))
	if err != nil {
		t.Fatalf("Stat errors/bad.crt: %v", err)
	}
	if errorInfo.Mode().Perm() != 0640 {
		t.Fatalf("errors/bad.crt perms = %o, want 640", errorInfo.Mode().Perm())
	}

	tsInfo, err := os.Stat(filepath.Join(dir, lastCompletedTimestampFile))
	if err != nil {
		t.Fatalf("Stat timestamp file: %v", err)
	}
	if tsInfo.Mode().Perm() != 0640 {
		t.Fatalf("timestamp perms = %o, want 640", tsInfo.Mode().Perm())
	}
}

func TestReceiverHonoursCustomFileMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	receiver := NewReceiver(logger, ReceiverConfig{
		RecvDir:  dir,
		FileMode: 0600,
	})

	if err := receiver.ReceiveFiles(map[string][]byte{
		"config.json": []byte(`{"secure":true}`),
	}); err != nil {
		t.Fatalf("ReceiveFiles: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("Stat config.json: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config.json perms = %o, want 600", info.Mode().Perm())
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package files

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodePem(t *testing.T) {
	t.Parallel()

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(privKey.Public())
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}

	validPrivKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyBytes,
	})

	validPubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	tests := []struct {
		name         string
		pemData      []byte
		filePath     string
		returnParsed bool
		wantErr      bool
		wantType     string
	}{
		{
			name:         "valid private key with parsing",
			pemData:      validPrivKeyPEM,
			filePath:     "test.key",
			returnParsed: true,
			wantErr:      false,
			wantType:     "ed25519.PrivateKey",
		},
		{
			name:         "valid private key validation only",
			pemData:      validPrivKeyPEM,
			filePath:     "test.key",
			returnParsed: false,
			wantErr:      false,
		},
		{
			name:         "valid public key with parsing",
			pemData:      validPubKeyPEM,
			filePath:     "test.pub",
			returnParsed: true,
			wantErr:      false,
			wantType:     "ed25519.PublicKey",
		},
		{
			name:         "valid public key validation only",
			pemData:      validPubKeyPEM,
			filePath:     "test.pub",
			returnParsed: false,
			wantErr:      false,
		},
		{
			name:         "invalid PEM data",
			pemData:      []byte("not pem data"),
			filePath:     "test.key",
			returnParsed: true,
			wantErr:      true,
		},
		{
			name: "unsupported PEM type",
			pemData: pem.EncodeToMemory(&pem.Block{
				Type:  "UNSUPPORTED",
				Bytes: []byte("test"),
			}),
			filePath:     "test.key",
			returnParsed: true,
			wantErr:      true,
		},
		{
			name: "malformed private key",
			pemData: pem.EncodeToMemory(&pem.Block{
				Type:  "PRIVATE KEY",
				Bytes: []byte("invalid key data"),
			}),
			filePath:     "test.key",
			returnParsed: true,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := DecodePem(tt.pemData, tt.filePath, tt.returnParsed)

			if tt.wantErr {
				if err == nil {
					t.Errorf("DecodePem() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("DecodePem() unexpected error: %v", err)
				return
			}

			if tt.returnParsed {
				if result == nil {
					t.Errorf("DecodePem() expected parsed result, got nil")
				}
			} else {
				if result != nil {
					t.Errorf("DecodePem() expected nil for validation only, got %v", result)
				}
			}
		})
	}
}

func TestLoadPEM(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	testPEMContent := `-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg7S8j1NKwrXe9PVHO
yTM7yCVyLLvJ8O3L9qnUm4NZgMShRANCAATq9/JJMFOLGhC7I3M8xS8x7K9OQp6N
BxGV0d5J7ZAzNhW4rFLs5N1wGtR0vL7nR8U/3+OsQzOoFfN7x0X8j2+L
-----END PRIVATE KEY-----`

	pemFile := filepath.Join(dir, "test.pem")
	if err := os.WriteFile(pemFile, []byte(testPEMContent), 0600); err != nil {
		t.Fatalf("failed to write PEM file: %v", err)
	}

	tests := []struct {
		name     string
		path     string
		file     string
		required bool
		wantErr  bool
	}{
		{
			name:     "load PEM file",
			path:     dir,
			file:     "test.pem",
			required: true,
			wantErr:  false,
		},
		{
			name:     "missing file required",
			path:     dir,
			file:     "missing.pem",
			required: true,
			wantErr:  true,
		},
		{
			name:     "missing file not required",
			path:     dir,
			file:     "missing.pem",
			required: false,
			wantErr:  false,
		},
		{
			name:     "invalid path",
			path:     "",
			file:     "test.pem",
			required: true,
			wantErr:  true,
		},
		{
			name:     "invalid filename",
			path:     dir,
			file:     "",
			required: true,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := LoadPEM(logger, tt.path, tt.file, tt.required)

			if tt.wantErr {
				if err == nil {
					t.Errorf("LoadPEM() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("LoadPEM() unexpected error: %v", err)
			}

			if tt.file == "test.pem" && result == nil {
				t.Errorf("LoadPEM() expected non-nil result for existing file")
			}
		})
	}
}

func TestLoadJWT(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	testJWTContent := "[REDACTED:jwt-token]   \n"

	jwtFile := filepath.Join(dir, "test.jwt")
	if err := os.WriteFile(jwtFile, []byte(testJWTContent), 0600); err != nil {
		t.Fatalf("failed to write JWT file: %v", err)
	}

	tests := []struct {
		name     string
		path     string
		file     string
		required bool
		wantErr  bool
	}{
		{
			name:     "load JWT file",
			path:     dir,
			file:     "test.jwt",
			required: true,
			wantErr:  false,
		},
		{
			name:     "missing file required",
			path:     dir,
			file:     "missing.jwt",
			required: true,
			wantErr:  true,
		},
		{
			name:     "missing file not required",
			path:     dir,
			file:     "missing.jwt",
			required: false,
			wantErr:  false,
		},
		{
			name:     "invalid path",
			path:     "",
			file:     "test.jwt",
			required: true,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := LoadJWT(logger, tt.path, tt.file, tt.required)

			if tt.wantErr {
				if err == nil {
					t.Errorf("LoadJWT() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("LoadJWT() unexpected error: %v", err)
				return
			}

			if tt.file == "test.jwt" {
				if strings.Contains(string(result), "\n") || strings.Contains(string(result), " ") {
					t.Errorf("LoadJWT() should trim whitespace, got: %q", string(result))
				}
			}
		})
	}
}

func TestLoadString(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	testStringContent := "test string content\n\n  "

	stringFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(stringFile, []byte(testStringContent), 0600); err != nil {
		t.Fatalf("failed to write string file: %v", err)
	}

	tests := []struct {
		name     string
		path     string
		file     string
		required bool
		wantErr  bool
	}{
		{
			name:     "load string file",
			path:     dir,
			file:     "test.txt",
			required: true,
			wantErr:  false,
		},
		{
			name:     "missing file required",
			path:     dir,
			file:     "missing.txt",
			required: true,
			wantErr:  true,
		},
		{
			name:     "missing file not required",
			path:     dir,
			file:     "missing.txt",
			required: false,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := LoadString(logger, tt.path, tt.file, tt.required)

			if tt.wantErr {
				if err == nil {
					t.Errorf("LoadString() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("LoadString() unexpected error: %v", err)
				return
			}

			if tt.file == "test.txt" {
				if strings.HasSuffix(result, "\n") || strings.HasSuffix(result, " ") {
					t.Errorf("LoadString() should trim whitespace, got: %q", result)
				}
			}
		})
	}
}

func TestWritePEM(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}

	validPEMData := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyBytes,
	}))

	tests := []struct {
		name    string
		path    string
		file    string
		data    string
		mode    os.FileMode
		wantErr bool
	}{
		{
			name:    "write valid PEM file",
			path:    dir,
			file:    "valid.pem",
			data:    validPEMData,
			mode:    0600,
			wantErr: false,
		},
		{
			name:    "invalid PEM data",
			path:    dir,
			file:    "invalid.pem",
			data:    "not pem data",
			mode:    0600,
			wantErr: true,
		},
		{
			name:    "invalid path",
			path:    "",
			file:    "test.pem",
			data:    validPEMData,
			mode:    0600,
			wantErr: true,
		},
		{
			name:    "invalid mode",
			path:    dir,
			file:    "test.pem",
			data:    validPEMData,
			mode:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WritePEM(tt.path, tt.file, tt.data, tt.mode)

			if tt.wantErr {
				if err == nil {
					t.Errorf("WritePEM() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("WritePEM() unexpected error: %v", err)
				return
			}

			if tt.path != "" && tt.file != "" {
				filePath := filepath.Join(tt.path, tt.file)
				info, err := os.Stat(filePath)
				if err != nil {
					t.Errorf("WritePEM() failed to create file: %v", err)
					return
				}

				if info.Mode().Perm() != tt.mode.Perm() {
					t.Errorf("WritePEM() mode = %o, want %o", info.Mode().Perm(), tt.mode.Perm())
				}
			}
		})
	}
}

func TestWriteString(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	tests := []struct {
		name    string
		path    string
		file    string
		data    string
		mode    os.FileMode
		wantErr bool
	}{
		{
			name:    "write string file",
			path:    dir,
			file:    "test.txt",
			data:    "  test content  \n\n",
			mode:    0644,
			wantErr: false,
		},
		{
			name:    "invalid path",
			path:    "",
			file:    "test.txt",
			data:    "test",
			mode:    0600,
			wantErr: true,
		},
		{
			name:    "invalid filename",
			path:    dir,
			file:    "",
			data:    "test",
			mode:    0600,
			wantErr: true,
		},
		{
			name:    "invalid mode",
			path:    dir,
			file:    "test.txt",
			data:    "test",
			mode:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WriteString(tt.path, tt.file, tt.data, tt.mode)

			if tt.wantErr {
				if err == nil {
					t.Errorf("WriteString() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("WriteString() unexpected error: %v", err)
				return
			}

			if tt.path != "" && tt.file != "" {
				filePath := filepath.Join(tt.path, tt.file)
				info, err := os.Stat(filePath)
				if err != nil {
					t.Errorf("WriteString() failed to create file: %v", err)
					return
				}

				if info.Mode().Perm() != tt.mode.Perm() {
					t.Errorf("WriteString() mode = %o, want %o", info.Mode().Perm(), tt.mode.Perm())
				}

				content, err := os.ReadFile(filePath)
				if err != nil {
					t.Errorf("WriteString() failed to read created file: %v", err)
					return
				}
				expectedContent := strings.TrimSpace(tt.data)
				if string(content) != expectedContent {
					t.Errorf("WriteString() content = %q, want %q", string(content), expectedContent)
				}
			}
		})
	}
}

func TestDeleteFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	testFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0600); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	testDir := filepath.Join(dir, "testdir")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		file    string
		wantErr bool
	}{
		{
			name:    "delete existing file",
			path:    dir,
			file:    "test.txt",
			wantErr: false,
		},
		{
			name:    "delete non-existent file",
			path:    dir,
			file:    "missing.txt",
			wantErr: false,
		},
		{
			name:    "try to delete directory",
			path:    dir,
			file:    "testdir",
			wantErr: true,
		},
		{
			name:    "invalid path",
			path:    "",
			file:    "test.txt",
			wantErr: true,
		},
		{
			name:    "invalid filename",
			path:    dir,
			file:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := DeleteFile(tt.path, tt.file)

			if tt.wantErr {
				if err == nil {
					t.Errorf("DeleteFile() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("DeleteFile() unexpected error: %v", err)
				return
			}

			if tt.path != "" && tt.file != "" && tt.file != "testdir" {
				filePath := filepath.Join(tt.path, tt.file)
				if _, err := os.Stat(filePath); !os.IsNotExist(err) {
					t.Errorf("DeleteFile() file still exists: %s", filePath)
				}
			}
		})
	}
}

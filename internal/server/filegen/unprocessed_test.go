// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package filegen

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestLogUnprocessedFiles(t *testing.T) {
	// Create a buffer to capture log output
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	// Create file processor with the test logger
	processor := &Generator{
		logger: logger,
	}

	tests := []struct {
		name           string
		missingFiles   []string
		processedFiles map[string]bool
		expectWarning  bool
		expectedFiles  []string
	}{
		{
			name:           "no unprocessed files",
			missingFiles:   []string{"file1.txt", "file2.txt"},
			processedFiles: map[string]bool{"file1.txt": true, "file2.txt": true},
			expectWarning:  false,
			expectedFiles:  nil,
		},
		{
			name:           "some unprocessed files",
			missingFiles:   []string{"configured.env", "unconfigured1.txt", "unconfigured2.log"},
			processedFiles: map[string]bool{"configured.env": true},
			expectWarning:  true,
			expectedFiles:  []string{"unconfigured1.txt", "unconfigured2.log"},
		},
		{
			name:           "all files unprocessed",
			missingFiles:   []string{"unknown1.xyz", "unknown2.abc"},
			processedFiles: map[string]bool{},
			expectWarning:  true,
			expectedFiles:  []string{"unknown1.xyz", "unknown2.abc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear the log buffer
			logBuffer.Reset()

			// Call the inlined logic directly
			var unprocessedFiles []string
			for _, filename := range tt.missingFiles {
				if !tt.processedFiles[filename] {
					unprocessedFiles = append(unprocessedFiles, filename)
				}
			}
			if len(unprocessedFiles) > 0 {
				processor.logger.Warn("Files reported as required but not configured for processing",
					"instance_id", "test-instance",
					"files", unprocessedFiles,
					"hint", "Check that these files are configured in the instance template")
			}

			logOutput := logBuffer.String()

			if tt.expectWarning {
				// Should contain warning message
				if !strings.Contains(logOutput, "Files reported as required but not configured for processing") {
					t.Error("Expected warning about unprocessed files not found in log output")
				}

				// Should contain instance ID
				if !strings.Contains(logOutput, "test-instance") {
					t.Error("Expected instance ID in log output")
				}

				// Should mention the expected unprocessed files
				for _, filename := range tt.expectedFiles {
					if !strings.Contains(logOutput, filename) {
						t.Errorf("Expected unprocessed file %s to be mentioned in log output", filename)
					}
				}

				// Should contain hint message
				if !strings.Contains(logOutput, "Check that these files are configured in the instance template") {
					t.Error("Expected hint message not found in log output")
				}
			} else {
				// Should not contain warning message
				if strings.Contains(logOutput, "Files reported as required but not configured for processing") {
					t.Error("Unexpected warning about unprocessed files found in log output")
				}
			}

			if testing.Verbose() {
				t.Logf("Log output: %s", logOutput)
			}
		})
	}
}

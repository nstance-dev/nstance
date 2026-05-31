// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package identifiers

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid simple",
			value:   "dev",
			wantErr: false,
		},
		{
			name:    "valid with hyphen",
			value:   "dev-1",
			wantErr: false,
		},
		{
			name:    "valid with multiple hyphens",
			value:   "us-west-2a",
			wantErr: false,
		},
		{
			name:    "valid single digit",
			value:   "0",
			wantErr: false,
		},
		{
			name:    "valid single letter",
			value:   "a",
			wantErr: false,
		},
		{
			name:    "valid alphanumeric mix",
			value:   "a1-b2-c3",
			wantErr: false,
		},
		{
			name:    "valid max length 32 chars",
			value:   "abcdefghijklmnopqrstuvwxyzabcdef",
			wantErr: false,
		},
		{
			name:    "invalid empty",
			value:   "",
			wantErr: true,
			errMsg:  "cannot be empty",
		},
		{
			name:    "invalid leading hyphen",
			value:   "-dev",
			wantErr: true,
			errMsg:  "no leading/trailing/consecutive hyphens",
		},
		{
			name:    "invalid trailing hyphen",
			value:   "dev-",
			wantErr: true,
			errMsg:  "no leading/trailing/consecutive hyphens",
		},
		{
			name:    "invalid uppercase",
			value:   "Dev",
			wantErr: true,
			errMsg:  "lowercase alphanumeric",
		},
		{
			name:    "invalid underscore",
			value:   "dev_1",
			wantErr: true,
			errMsg:  "lowercase alphanumeric",
		},
		{
			name:    "invalid exceeds max length",
			value:   "abcdefghijklmnopqrstuvwxyzabcdefg",
			wantErr: true,
			errMsg:  "exceeds maximum length",
		},
		{
			name:    "invalid space",
			value:   "dev 1",
			wantErr: true,
			errMsg:  "lowercase alphanumeric",
		},
		{
			name:    "invalid double hyphen",
			value:   "dev--1",
			wantErr: true,
			errMsg:  "no leading/trailing/consecutive hyphens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate("test identifier", tt.value)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate(%q) expected error, got nil", tt.value)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate(%q) error = %v, want error containing %q", tt.value, err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Validate(%q) unexpected error: %v", tt.value, err)
				}
			}
		})
	}
}

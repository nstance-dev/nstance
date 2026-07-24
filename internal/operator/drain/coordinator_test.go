// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package drain

import (
	"testing"

	"github.com/nstance-dev/nstance/internal/operator/node"
)

func TestMatchesProviderID(t *testing.T) {
	tests := []struct {
		name               string
		nodeProviderID     string
		providerInstanceID string
		want               bool
	}{
		{
			name:               "direct match",
			nodeProviderID:     "i-1234567890abcdef0",
			providerInstanceID: "i-1234567890abcdef0",
			want:               true,
		},
		{
			name:               "aws standard format",
			nodeProviderID:     "aws:///us-west-2a/i-1234567890abcdef0",
			providerInstanceID: "i-1234567890abcdef0",
			want:               true,
		},
		{
			name:               "aws format with account id",
			nodeProviderID:     "aws://123456789012/us-west-2a/i-1234567890abcdef0",
			providerInstanceID: "i-1234567890abcdef0",
			want:               true,
		},
		{
			name:               "google format",
			nodeProviderID:     "gce://project/zone/instance-name",
			providerInstanceID: "instance-name",
			want:               true,
		},

		{
			name:               "mismatch instance id",
			nodeProviderID:     "aws:///us-west-2a/i-1234567890abcdef0",
			providerInstanceID: "i-0987654321fedcba0",
			want:               false,
		},
		{
			name:               "mismatch provider type",
			nodeProviderID:     "gce://project/zone/instance-name",
			providerInstanceID: "i-1234567890abcdef0",
			want:               false,
		},
		{
			name:               "empty node provider id",
			nodeProviderID:     "",
			providerInstanceID: "i-1234567890abcdef0",
			want:               false,
		},
		{
			name:               "empty provider instance id",
			nodeProviderID:     "aws:///us-west-2a/i-1234567890abcdef0",
			providerInstanceID: "",
			want:               false,
		},
		{
			name:               "unknown prefix mismatch",
			nodeProviderID:     "unknown://something/id",
			providerInstanceID: "id",
			want:               false,
		},
		{
			name:               "malformed aws id",
			nodeProviderID:     "aws://",
			providerInstanceID: "id",
			want:               false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := node.MatchesProviderID(tt.nodeProviderID, tt.providerInstanceID)
			if got != tt.want {
				t.Errorf("MatchesProviderID(%q, %q) = %v, want %v", tt.nodeProviderID, tt.providerInstanceID, got, tt.want)
			}
		})
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package gcp

import (
	"strings"
)

// GCP label keys for instance metadata.
// GCP labels must be lowercase with hyphens (no colons allowed).
// Uses "nstance-" prefix.
const (
	labelInstanceID     = "nstance-instance-id"
	labelInstanceKind   = "nstance-instance-kind"
	labelNstanceManaged = "nstance-managed"
	labelTemplate       = "nstance-template"
	labelGroup          = "nstance-group"
	labelClusterID      = "nstance-cluster-id"
	labelShard          = "nstance-shard"
)

// sanitizeLabel converts a value to valid GCP label format (lowercase, hyphens only)
func sanitizeLabel(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

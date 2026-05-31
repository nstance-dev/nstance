// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package aws

// AWS tag keys for EC2 instance metadata.
// Following AWS best practices: lowercase, hyphens, namespaced with "nstance:" prefix.
// See: https://docs.aws.amazon.com/tag-editor/latest/userguide/best-practices-and-strats.html
const (
	tagInstanceID     = "nstance:instance-id"
	tagInstanceKind   = "nstance:instance-kind"
	tagNstanceManaged = "nstance:managed"
	tagTemplate       = "nstance:template"
	tagGroup          = "nstance:group"
	tagClusterID      = "nstance:cluster-id"
	tagShard          = "nstance:shard"
)

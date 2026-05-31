// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

// Package instances orchestrates instance lifecycle: generating IDs, registration
// nonce JWTs, and userdata, then delegating VM creation to the infra package.
// It is the single writer for instance/{shard}/*.json records in S3, using
// read-modify-write to update registration data after initial creation.
package instances

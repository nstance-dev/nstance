// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

// Package gc provides periodic garbage collection and maintenance tasks for nstance instances.
//
// The garbage collector runs periodically (controlled by the interval config) and performs:
//
//   - Provider sync: Fetches current instance state from the cloud provider and backfills
//     the local database with any instances not yet tracked.
//
//   - Dangling instance cleanup: Terminates instances that were created but never registered
//     with the server within the registration timeout period.
//
//   - Health monitoring: Detects instances with stale health reports and enqueues them
//     for reconciliation checks.
//
//   - Deleted record cleanup: Purges storage records for instances that have been deleted
//     for longer than the configured retention period.
//
// The gc Runner orchestrates these tasks and only runs when the server is the shard leader.
package gc

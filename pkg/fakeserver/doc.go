// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

// Package fakeserver provides a lightweight fake/test Nstance server which
// implements the agent registration and file-delivery gRPC APIs. It is meant
// for test/dev harnesses that need to bootstrap agents without running the
// full nstance-server control plane, database, provider, reconciler, and
// operator APIs.
package fakeserver

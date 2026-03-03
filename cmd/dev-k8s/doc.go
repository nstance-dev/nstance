// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

// Package main implements a lightweight fake Kubernetes API server for development.
//
// This server provides file-backed storage with watch support, sufficient to run
// the nstance-operator locally without a real Kubernetes cluster. Resources are
// stored as JSON files under temp/dev-k8s/ organized by resource type and namespace.
//
// Usage:
//
//	go run ./cmd/dev-k8s
//	go run ./cmd/dev-k8s -addr :6443 -dir temp/dev-k8s
//
// The server supports:
//   - CRUD operations for Secrets, ConfigMaps, and custom resources
//   - Watch API via fsnotify file watching
//   - Status subresource updates
//   - MD5-based resourceVersion for conflict detection
package main

# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

output "id" {
  description = "The unique cluster identifier"
  value       = local.cluster_id
}

output "name_prefix" {
  description = "Name prefix for resources"
  value       = var.name_prefix
}

output "shards" {
  description = "List of valid shard IDs (empty if not specified)"
  value       = var.shards
}

output "bucket" {
  description = "GCS bucket name for nstance storage"
  value       = local.create_bucket ? google_storage_bucket.nstance[0].name : var.bucket
}

output "bucket_name" {
  description = "GCS bucket name for nstance storage (alias for bucket)"
  value       = local.create_bucket ? google_storage_bucket.nstance[0].name : var.bucket
}

output "project_id" {
  description = "Google Cloud project ID"
  value       = data.google_client_config.current.project
}

output "secrets_provider" {
  description = "The secrets provider type"
  value       = local.secrets_provider
}

output "encryption_key_provider" {
  description = "Provider holding the object-storage encryption key"
  value       = local.secrets_provider == "object-storage" ? local.encryption_key_provider : ""
}

output "encryption_key_source" {
  description = "Encryption key source identifier for the secrets store"
  value       = local.secrets_provider == "object-storage" ? (local.create_encryption_key ? google_secret_manager_secret.encryption_key[0].secret_id : var.encryption_key) : ""
}

output "server_config" {
  description = "Server configuration with defaults applied"
  value       = local.server_config
}

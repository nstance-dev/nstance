# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

output "id" {
  description = "Cluster ID"
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
  description = "S3 bucket name"
  value       = local.create_bucket ? aws_s3_bucket.nstance[0].id : data.aws_s3_bucket.existing[0].id
}

output "bucket_arn" {
  description = "S3 bucket ARN"
  value       = local.create_bucket ? aws_s3_bucket.nstance[0].arn : data.aws_s3_bucket.existing[0].arn
}

output "secrets_provider" {
  description = "Secrets storage provider (object-storage or aws-secrets-manager)"
  value       = var.secrets_provider
}

output "encryption_key_source" {
  description = "Encryption key source identifier for the secrets store"
  value       = var.secrets_provider == "object-storage" ? (local.create_encryption_key ? aws_secretsmanager_secret.encryption_key[0].arn : var.encryption_key) : ""
}

output "server_config" {
  description = "Server configuration (defaults merged with user overrides)"
  value       = local.server_config
}

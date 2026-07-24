# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
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
  description = "Secrets storage provider"
  value       = local.secrets_provider
}

output "secrets_prefix" {
  description = "Explicit prefix for direct cloud secret names"
  value       = var.secrets_prefix
}

output "encryption_key_provider" {
  description = "Provider holding the object-storage encryption key"
  value       = local.secrets_provider == "object-storage" ? local.encryption_key_provider : ""
}

output "encryption_key_source" {
  description = "Encryption key source identifier for the secrets store"
  value = local.secrets_provider == "object-storage" ? (
    local.create_encryption_key ? one(concat(aws_ssm_parameter.encryption_key[*].name, aws_secretsmanager_secret.encryption_key[*].arn)) : var.encryption_key
  ) : ""
}

output "server_config" {
  description = "Server configuration (defaults merged with user overrides)"
  value       = local.server_config
}

# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

data "aws_region" "current" {}

locals {
  cluster_id            = var.cluster_id
  create_bucket         = var.bucket == ""
  create_encryption_key = var.secrets_provider == "object-storage" && var.encryption_key == ""
  profile_flag          = var.aws_profile != null ? "--profile ${var.aws_profile}" : ""
  server_config         = var.server_config
}

resource "random_id" "bucket_suffix" {
  count       = local.create_bucket ? 1 : 0
  byte_length = 4
}

# S3 bucket for nstance config/state storage (shared across all shards)
resource "aws_s3_bucket" "nstance" {
  count  = local.create_bucket ? 1 : 0
  bucket = "${var.name_prefix}-${random_id.bucket_suffix[0].hex}"

  tags = merge(var.tags, {
    Name = "${var.name_prefix}-bucket"
  })
}

resource "aws_s3_bucket_versioning" "nstance" {
  count  = local.create_bucket && var.versioning ? 1 : 0
  bucket = aws_s3_bucket.nstance[0].id
  versioning_configuration {
    status = "Enabled"
  }
}

# Block public access
resource "aws_s3_bucket_public_access_block" "nstance" {
  count  = local.create_bucket ? 1 : 0
  bucket = aws_s3_bucket.nstance[0].id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Server-side encryption
resource "aws_s3_bucket_server_side_encryption_configuration" "nstance" {
  count  = local.create_bucket ? 1 : 0
  bucket = aws_s3_bucket.nstance[0].id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Lookup existing bucket if provided
data "aws_s3_bucket" "existing" {
  count  = local.create_bucket ? 0 : 1
  bucket = var.bucket
}

# Create secret in Secrets Manager (shared across all shards, value generated externally)
resource "aws_secretsmanager_secret" "encryption_key" {
  count                   = local.create_encryption_key ? 1 : 0
  name                    = "nstance/${var.name_prefix}/encryption-key"
  description             = "Encryption key for Nstance Server"
  recovery_window_in_days = 7

  tags = merge(var.tags, {
    Name = "${var.name_prefix}-encryption-key"
  })
}

# Initialize secret value using AWS CLI (only if not already set)
# This keeps the secret value out of Terraform state
resource "null_resource" "encryption_key_init" {
  count = local.create_encryption_key ? 1 : 0

  triggers = {
    secret_arn = aws_secretsmanager_secret.encryption_key[0].arn
  }

  provisioner "local-exec" {
    command = <<-EOF
      set -e
      if ! aws secretsmanager get-secret-value \
        --secret-id "${aws_secretsmanager_secret.encryption_key[0].id}" \
        --region "${data.aws_region.current.region}" ${local.profile_flag} 2>/dev/null; then
        PASSWORD=$(aws secretsmanager get-random-password \
          --password-length 32 \
          --exclude-punctuation \
          --region "${data.aws_region.current.region}" ${local.profile_flag} \
          --query RandomPassword \
          --output text)
        aws secretsmanager put-secret-value \
          --secret-id "${aws_secretsmanager_secret.encryption_key[0].id}" \
          --secret-string "$PASSWORD" \
          --region "${data.aws_region.current.region}" ${local.profile_flag}
        echo "Encryption key initialized"
      else
        echo "Encryption key already exists, skipping initialization"
      fi
    EOF
  }
}

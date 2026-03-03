# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

data "aws_region" "current" {}

locals {
  cluster_id            = var.cluster_id
  create_bucket         = var.bucket == ""
  create_encryption_key = var.secrets_provider == "object-storage" && var.encryption_key == ""
  profile_flag          = var.aws_profile != null ? "--profile ${var.aws_profile}" : ""

  default_server_config = {
    request_timeout        = "30s"
    create_rate_limit      = "100ms"
    health_check_interval  = "60s"
    default_drain_timeout  = "5m"
    image_refresh_interval = "6h"
    shutdown_timeout       = "10s"

    garbage_collection = {
      interval                 = "2m"
      registration_timeout     = "5m"
      deleted_record_retention = "30m"
    }

    leader_election = {
      frequent_interval   = "5s"
      infrequent_interval = "30s"
      leader_timeout      = "15s"
    }

    expiry = {
      eligible_age = ""
      forced_age   = ""
      ondemand_age = ""
    }

    error_exit_jitter = {
      min_delay = "10s"
      max_delay = "40s"
    }

    bind = {
      health_addr       = "0.0.0.0:8990"
      election_addr     = "0.0.0.0:8991"
      registration_addr = "0.0.0.0:8992"
      operator_addr     = "0.0.0.0:8993"
      agent_addr        = "0.0.0.0:8994"
    }

    advertise = {
      health_addr       = ":8990"
      election_addr     = ":8991"
      registration_addr = ":8992"
      operator_addr     = ":8993"
      agent_addr        = ":8994"
    }
  }

  server_config = {
    request_timeout        = coalesce(var.server_config.request_timeout, local.default_server_config.request_timeout)
    create_rate_limit      = coalesce(var.server_config.create_rate_limit, local.default_server_config.create_rate_limit)
    health_check_interval  = coalesce(var.server_config.health_check_interval, local.default_server_config.health_check_interval)
    default_drain_timeout  = coalesce(var.server_config.default_drain_timeout, local.default_server_config.default_drain_timeout)
    image_refresh_interval = coalesce(var.server_config.image_refresh_interval, local.default_server_config.image_refresh_interval)
    shutdown_timeout       = coalesce(var.server_config.shutdown_timeout, local.default_server_config.shutdown_timeout)

    garbage_collection = {
      interval                 = coalesce(try(var.server_config.garbage_collection.interval, null), local.default_server_config.garbage_collection.interval)
      registration_timeout     = coalesce(try(var.server_config.garbage_collection.registration_timeout, null), local.default_server_config.garbage_collection.registration_timeout)
      deleted_record_retention = coalesce(try(var.server_config.garbage_collection.deleted_record_retention, null), local.default_server_config.garbage_collection.deleted_record_retention)
    }

    leader_election = {
      frequent_interval   = coalesce(try(var.server_config.leader_election.frequent_interval, null), local.default_server_config.leader_election.frequent_interval)
      infrequent_interval = coalesce(try(var.server_config.leader_election.infrequent_interval, null), local.default_server_config.leader_election.infrequent_interval)
      leader_timeout      = coalesce(try(var.server_config.leader_election.leader_timeout, null), local.default_server_config.leader_election.leader_timeout)
    }

    expiry = {
      # Expiry ages default to empty string (disabled) - use try/null fallback pattern since coalesce rejects empty strings
      eligible_age = try(var.server_config.expiry.eligible_age, null) != null ? var.server_config.expiry.eligible_age : local.default_server_config.expiry.eligible_age
      forced_age   = try(var.server_config.expiry.forced_age, null) != null ? var.server_config.expiry.forced_age : local.default_server_config.expiry.forced_age
      ondemand_age = try(var.server_config.expiry.ondemand_age, null) != null ? var.server_config.expiry.ondemand_age : local.default_server_config.expiry.ondemand_age
    }

    error_exit_jitter = {
      min_delay = coalesce(try(var.server_config.error_exit_jitter.min_delay, null), local.default_server_config.error_exit_jitter.min_delay)
      max_delay = coalesce(try(var.server_config.error_exit_jitter.max_delay, null), local.default_server_config.error_exit_jitter.max_delay)
    }

    bind = {
      health_addr       = coalesce(try(var.server_config.bind.health_addr, null), local.default_server_config.bind.health_addr)
      election_addr     = coalesce(try(var.server_config.bind.election_addr, null), local.default_server_config.bind.election_addr)
      registration_addr = coalesce(try(var.server_config.bind.registration_addr, null), local.default_server_config.bind.registration_addr)
      operator_addr     = coalesce(try(var.server_config.bind.operator_addr, null), local.default_server_config.bind.operator_addr)
      agent_addr        = coalesce(try(var.server_config.bind.agent_addr, null), local.default_server_config.bind.agent_addr)
    }

    advertise = {
      health_addr       = coalesce(try(var.server_config.advertise.health_addr, null), local.default_server_config.advertise.health_addr)
      election_addr     = coalesce(try(var.server_config.advertise.election_addr, null), local.default_server_config.advertise.election_addr)
      registration_addr = coalesce(try(var.server_config.advertise.registration_addr, null), local.default_server_config.advertise.registration_addr)
      operator_addr     = coalesce(try(var.server_config.advertise.operator_addr, null), local.default_server_config.advertise.operator_addr)
      agent_addr        = coalesce(try(var.server_config.advertise.agent_addr, null), local.default_server_config.advertise.agent_addr)
    }
  }
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
        --region "${data.aws_region.current.id}" ${local.profile_flag} 2>/dev/null; then
        PASSWORD=$(aws secretsmanager get-random-password \
          --password-length 32 \
          --exclude-punctuation \
          --region "${data.aws_region.current.id}" ${local.profile_flag} \
          --query RandomPassword \
          --output text)
        aws secretsmanager put-secret-value \
          --secret-id "${aws_secretsmanager_secret.encryption_key[0].id}" \
          --secret-string "$PASSWORD" \
          --region "${data.aws_region.current.id}" ${local.profile_flag}
        echo "Encryption key initialized"
      else
        echo "Encryption key already exists, skipping initialization"
      fi
    EOF
  }
}

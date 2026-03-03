# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

data "google_client_config" "current" {}

# Required GCP APIs - auto-enabled on first apply
resource "google_project_service" "required" {
  for_each = toset([
    "compute.googleapis.com",       # VMs, networks, firewalls
    "secretmanager.googleapis.com", # Encryption key storage
    "storage.googleapis.com",       # GCS bucket for state
    "iam.googleapis.com",           # Service accounts
    "iap.googleapis.com",           # IAP SSH access
  ])

  project            = data.google_client_config.current.project
  service            = each.value
  disable_on_destroy = false
}

locals {
  cluster_id            = var.cluster_id
  create_bucket         = var.bucket == ""
  create_encryption_key = var.secrets_provider == "object-storage" && var.encryption_key == ""

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

resource "google_storage_bucket" "nstance" {
  count    = local.create_bucket ? 1 : 0
  name     = "${var.name_prefix}-${random_id.bucket_suffix[0].hex}"
  location = data.google_client_config.current.region
  project  = data.google_client_config.current.project

  uniform_bucket_level_access = true

  versioning {
    enabled = var.versioning
  }

  public_access_prevention = "enforced"

  labels = var.tags

  depends_on = [google_project_service.required]
}

resource "google_secret_manager_secret" "encryption_key" {
  count     = local.create_encryption_key ? 1 : 0
  secret_id = "${var.name_prefix}-encryption-key"
  project   = data.google_client_config.current.project

  replication {
    auto {}
  }

  labels = var.tags

  depends_on = [google_project_service.required]
}

resource "null_resource" "encryption_key_init" {
  count = local.create_encryption_key ? 1 : 0

  triggers = {
    secret_id = google_secret_manager_secret.encryption_key[0].id
  }

  provisioner "local-exec" {
    command = <<-EOF
      set -e
      if ! gcloud secrets versions access latest \
        --secret="${google_secret_manager_secret.encryption_key[0].secret_id}" \
        --project="${data.google_client_config.current.project}" 2>/dev/null; then
        PASSWORD=$(openssl rand -base64 32 | tr -d '/+=' | head -c 32)
        echo -n "$PASSWORD" | gcloud secrets versions add \
          "${google_secret_manager_secret.encryption_key[0].secret_id}" \
          --data-file=- \
          --project="${data.google_client_config.current.project}"
        echo "Encryption key initialized"
      else
        echo "Encryption key already exists, skipping initialization"
      fi
    EOF
  }
}

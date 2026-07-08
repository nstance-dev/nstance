# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

data "google_client_config" "current" {}

# Required Google Cloud APIs - auto-enabled on first apply
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
  server_config         = var.server_config
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

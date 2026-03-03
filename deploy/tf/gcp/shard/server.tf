# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# ============================================================================
# Server Leader IP (Reserved internal IP, assigned as alias)
# ============================================================================

resource "google_compute_address" "server_leader" {
  name         = "${local.name_prefix}-server-leader-ip-${var.shard}"
  subnetwork   = local.server_subnet_id
  address_type = "INTERNAL"
  region       = local.region

  depends_on = [terraform_data.validate_server_subnet]
}

# ============================================================================
# Server Image (Debian)
# ============================================================================

data "google_compute_image" "debian_amd64" {
  family  = "debian-13"
  project = "debian-cloud"
}

data "google_compute_image" "debian_arm64" {
  family  = "debian-13-arm64"
  project = "debian-cloud"
}

# ============================================================================
# Server Userdata
# ============================================================================

locals {
  server_image = local.server_arch == "arm64" ? data.google_compute_image.debian_arm64.self_link : data.google_compute_image.debian_amd64.self_link

  server_userdata = templatefile("${path.module}/templates/server-userdata.sh.tpl", {
    nstance_version = local.nstance_version
    github_repo     = local.github_repo
    binary_url      = var.nstance_server_binary_url
    provider        = "gcp"
    storage         = "gcs"
    aws_region      = ""
    gcp_project     = local.project_id
    bucket          = var.cluster.bucket
    shard           = var.shard
    enable_ssm      = false
  })
}

# ============================================================================
# Server Instance Template
# ============================================================================

resource "google_compute_instance_template" "server" {
  name_prefix  = "${local.name_prefix}-server-${var.shard}-"
  machine_type = var.server_machine_type
  region       = local.region

  disk {
    source_image = local.server_image
    auto_delete  = true
    boot         = true
    disk_size_gb = 20
    disk_type    = "pd-balanced"
  }

  network_interface {
    subnetwork = local.server_subnet_id
  }

  service_account {
    email  = var.account.server_iam_role_arn
    scopes = ["cloud-platform"]
  }

  metadata = merge(
    {
      startup-script = local.server_userdata
    },
    var.enable_os_login ? {
      enable-oslogin         = "TRUE"
      block-project-ssh-keys = "TRUE"
    } : {},
    length(var.ssh_keys) > 0 ? {
      ssh-keys = join("\n", var.ssh_keys)
    } : {}
  )

  tags = ["nstance-server-${var.shard}"]

  labels = local.common_tags

  lifecycle {
    create_before_destroy = true
  }
}

# ============================================================================
# Server Managed Instance Group
# ============================================================================

resource "google_compute_instance_group_manager" "server" {
  name               = "${local.name_prefix}-server-mig-${var.shard}"
  base_instance_name = "${local.name_prefix}-server-${var.shard}"
  zone               = var.zone
  target_size        = var.server_count

  version {
    instance_template = google_compute_instance_template.server.id
  }

  update_policy {
    type                           = "PROACTIVE"
    minimal_action                 = "REPLACE"
    most_disruptive_allowed_action = "REPLACE"
    max_surge_fixed                = 1
    max_unavailable_fixed          = 0
  }

  depends_on = [
    google_storage_bucket_object.shard_config
  ]
}

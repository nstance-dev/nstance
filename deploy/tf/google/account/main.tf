# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

locals {
  name_prefix = var.cluster.name_prefix
}

# ============================================================================
# Nstance Server Service Account
# ============================================================================

resource "google_service_account" "server" {
  account_id   = "${local.name_prefix}-server"
  display_name = "Nstance Server Service Account"
}

# GCS bucket access for server
resource "google_storage_bucket_iam_member" "server_bucket_admin" {
  bucket = var.cluster.bucket_name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.server.email}"
}

# Secret Manager access for an object-storage encryption key.
resource "google_secret_manager_secret_iam_member" "server_secret_accessor" {
  count = var.cluster.secrets_provider == "object-storage" && var.cluster.encryption_key_provider == "google-secret-manager" ? 1 : 0

  secret_id = var.cluster.encryption_key_source
  project   = var.cluster.project_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.server.email}"
}

# Minimal project-level role for the direct Secret Manager store. Secret creation
# cannot be restricted to a name prefix because its IAM resource is the project.
resource "google_project_iam_custom_role" "server_secret_store" {
  count = var.cluster.secrets_provider == "google-secret-manager" ? 1 : 0

  project     = var.cluster.project_id
  role_id     = "${replace(local.name_prefix, "-", "_")}_secret_store"
  title       = "Nstance Secret Store"
  description = "Manage Nstance secrets in Secret Manager"
  permissions = [
    "secretmanager.secrets.create",
    "secretmanager.secrets.delete",
    "secretmanager.secrets.get",
    "secretmanager.secrets.list",
    "secretmanager.versions.access",
    "secretmanager.versions.add",
  ]
}

resource "google_project_iam_member" "server_secret_store" {
  count = var.cluster.secrets_provider == "google-secret-manager" ? 1 : 0

  project = var.cluster.project_id
  role    = google_project_iam_custom_role.server_secret_store[0].id
  member  = "serviceAccount:${google_service_account.server.email}"
}

# Compute Instance Admin for creating/managing agent VMs
resource "google_project_iam_member" "server_compute_admin" {
  project = var.cluster.project_id
  role    = "roles/compute.instanceAdmin.v1"
  member  = "serviceAccount:${google_service_account.server.email}"
}

# Network User for creating instances in subnets
resource "google_project_iam_member" "server_network_user" {
  project = var.cluster.project_id
  role    = "roles/compute.networkUser"
  member  = "serviceAccount:${google_service_account.server.email}"
}

# Nstance owns only dynamic NEG membership and Nstance-tagged NAT routes.
resource "google_project_iam_custom_role" "server_network_control" {
  project     = var.cluster.project_id
  role_id     = "${replace(local.name_prefix, "-", "_")}_network_control"
  title       = "Nstance Network Control"
  description = "Manage Nstance load-balancer membership and NAT routes"
  permissions = [
    "compute.forwardingRules.list",
    "compute.networkEndpointGroups.attachNetworkEndpoints",
    "compute.networkEndpointGroups.detachNetworkEndpoints",
    "compute.networkEndpointGroups.get",
    "compute.networkEndpointGroups.list",
    "compute.networkEndpointGroups.use",
    "compute.routes.create",
    "compute.routes.delete",
    "compute.routes.get",
  ]
}

resource "google_project_iam_member" "server_network_control" {
  project = var.cluster.project_id
  role    = google_project_iam_custom_role.server_network_control.id
  member  = "serviceAccount:${google_service_account.server.email}"
}

# Service Account User to attach agent service account to VMs
resource "google_service_account_iam_member" "server_can_use_agent_sa" {
  service_account_id = google_service_account.agent.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.server.email}"
}

# ============================================================================
# Nstance Agent Service Account
# ============================================================================

resource "google_service_account" "agent" {
  account_id   = "${local.name_prefix}-agent"
  display_name = "Nstance Agent Service Account"
}

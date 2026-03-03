# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# ============================================================================
# Server Firewall Rules
# ============================================================================

# Health check endpoint (HTTP)
resource "google_compute_firewall" "server_health" {
  name    = "${local.name_prefix}-server-health-${var.shard}"
  network = var.network.vpc_id

  allow {
    protocol = "tcp"
    ports    = [local.health_port]
  }

  source_ranges = [var.network.vpc_cidr_ipv4]
  target_tags   = ["nstance-server-${var.shard}"]
}

# gRPC APIs (leader election, registration, operator, agent)
resource "google_compute_firewall" "server_grpc" {
  name    = "${local.name_prefix}-server-grpc-${var.shard}"
  network = var.network.vpc_id

  allow {
    protocol = "tcp"
    ports    = ["${local.election_port}-${local.agent_port}"]
  }

  source_ranges = [var.network.vpc_cidr_ipv4]
  target_tags   = ["nstance-server-${var.shard}"]
}

# ============================================================================
# IAP SSH Access
# ============================================================================

# Allow SSH from IAP tunnel range only (when IAP is enabled)
resource "google_compute_firewall" "iap_ssh" {
  count = var.enable_iap ? 1 : 0

  name    = "${local.name_prefix}-iap-ssh-${var.shard}"
  network = var.network.vpc_id

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  # IAP's TCP forwarding uses this IP range
  source_ranges = ["35.235.240.0/20"]
  target_tags   = ["nstance-server-${var.shard}", "nstance-agent-${var.shard}"]
}

# ============================================================================
# Internal Communication
# ============================================================================

# Allow all internal communication within the VPC
resource "google_compute_firewall" "internal" {
  name    = "${local.name_prefix}-internal-${var.shard}"
  network = var.network.vpc_id

  allow {
    protocol = "tcp"
  }

  allow {
    protocol = "udp"
  }

  allow {
    protocol = "icmp"
  }

  source_ranges = [var.network.vpc_cidr_ipv4]
}

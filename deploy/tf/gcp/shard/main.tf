# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# GCP Shard Module
# This module creates a single shard on GCP including:
# - Server subnet and group subnets
# - Firewall rules for server and agents
# - Server instances

data "google_client_config" "current" {}

data "google_project" "current" {}

locals {
  project_id  = data.google_client_config.current.project
  region      = regex("^([a-z]+-[a-z]+[0-9]+)", var.zone)[0]
  name_prefix = coalesce(var.name_prefix, var.cluster.name_prefix)
  shards      = var.cluster.shards

  # Extract port numbers from addr strings for firewall rules and leader-service binds
  registration_port = tonumber(element(split(":", var.cluster.server_config.bind.registration_addr), length(split(":", var.cluster.server_config.bind.registration_addr)) - 1))
  operator_port     = tonumber(element(split(":", var.cluster.server_config.bind.operator_addr), length(split(":", var.cluster.server_config.bind.operator_addr)) - 1))
  agent_port        = tonumber(element(split(":", var.cluster.server_config.bind.agent_addr), length(split(":", var.cluster.server_config.bind.agent_addr)) - 1))
  health_port       = tonumber(element(split(":", var.cluster.server_config.bind.health_addr), length(split(":", var.cluster.server_config.bind.health_addr)) - 1))
  election_port     = tonumber(element(split(":", var.cluster.server_config.bind.election_addr), length(split(":", var.cluster.server_config.bind.election_addr)) - 1))

  # Apply provider-specific default for server_arch
  server_arch = coalesce(var.server_arch, "amd64")

  # Filter subnets for this shard based on shard and zone.
  # A subnet is included if:
  # 1. The subnet is in the shard's zone, AND
  # 2. Either: no shards filter (empty list), OR the shard is in the shards list
  filtered_subnets = {
    for role, zones in var.network.subnets : role => flatten([
      for zone, entries in zones : [
        for entry in entries : entry.id
        if zone == var.zone && (length(try(entry.shards, [])) == 0 || contains(try(entry.shards, []), var.shard))
      ]
    ])
    if length(flatten([
      for zone, entries in zones : [
        for entry in entries : entry.id
        if zone == var.zone && (length(try(entry.shards, [])) == 0 || contains(try(entry.shards, []), var.shard))
      ]
    ])) > 0
  }

  # Count total subnets found for validation
  total_filtered_subnets = sum([for role, ids in local.filtered_subnets : length(ids)])

  # Resolve server subnet ID from the server_subnet role
  server_subnet_id = try(local.filtered_subnets[var.server_subnet][0], "")

  # Common tags applied to all resources
  common_tags = merge(
    {
      "nstance-managed"    = "true"
      "nstance-cluster-id" = var.cluster.id
      "nstance-shard"      = var.shard
    },
    var.tags
  )

}

# Validate that at least one subnet was found (catches shard/zone typos)
resource "terraform_data" "validate_subnets" {
  lifecycle {
    precondition {
      condition     = local.total_filtered_subnets > 0
      error_message = "No subnets found for shard '${var.shard}' in zone '${var.zone}'. Check that the zone exists in the network module's subnets configuration and that shard matches any shard filters."
    }
  }
}

# Validate shard ID is in cluster.shards (when shards list is non-empty)
resource "terraform_data" "validate_shard" {
  count = length(local.shards) > 0 ? 1 : 0

  lifecycle {
    precondition {
      condition     = contains(local.shards, var.shard)
      error_message = "Shard '${var.shard}' is not in cluster.shards list: ${jsonencode(local.shards)}"
    }
  }
}

# Validate that the server_subnet role exists and has at least one subnet
resource "terraform_data" "validate_server_subnet" {
  lifecycle {
    precondition {
      condition     = contains(keys(local.filtered_subnets), var.server_subnet)
      error_message = "Server subnet role '${var.server_subnet}' not found. Available roles: ${join(", ", keys(local.filtered_subnets))}. Define it in network.subnets."
    }
    precondition {
      condition     = length(try(local.filtered_subnets[var.server_subnet], [])) > 0
      error_message = "No subnets found for server role '${var.server_subnet}' in zone '${var.zone}'."
    }
  }
}

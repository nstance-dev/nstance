# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

data "google_client_config" "current" {}

locals {
  project_id  = var.project_id != "" ? var.project_id : data.google_client_config.current.project
  region      = var.region != "" ? var.region : data.google_client_config.current.region
  name_prefix = coalesce(var.name_prefix, var.cluster.name_prefix)
  shards      = var.cluster.shards

  # Use existing VPC or create new one
  use_existing_vpc = var.vpc_id != ""
  vpc_id           = local.use_existing_vpc ? var.vpc_id : google_compute_network.main[0].self_link
  vpc_ipv6_cidr    = local.use_existing_vpc ? null : (var.enable_ipv6 ? google_compute_network.main[0].internal_ipv6_range : null)
}

# VPC Network with optional dual-stack (IPv4 + IPv6) - only when not using existing VPC
resource "google_compute_network" "main" {
  count = local.use_existing_vpc ? 0 : 1

  name                     = "${local.name_prefix}-vpc"
  project                  = local.project_id
  auto_create_subnetworks  = false
  routing_mode             = "REGIONAL"
  enable_ula_internal_ipv6 = var.enable_ipv6
  internal_ipv6_range      = var.enable_ipv6 ? null : null # Auto-assigned when enable_ula_internal_ipv6 is true
}

# Flatten subnets: role -> zone -> list -> individual subnet definitions
# Key format: "role/zone/index"
locals {
  subnet_definitions = merge([
    for role, zones in var.subnets : merge([
      for zone, defs in zones : {
        for idx, def in defs : "${role}/${zone}/${idx}" => {
          role        = role
          zone        = zone
          index       = idx
          existing    = try(def.existing, null) != null
          name        = try(def.existing, null)
          ipv4_cidr   = try(def.ipv4_cidr, null)
          ipv6_netnum = try(def.ipv6_netnum, null)
          ipv6_cidr   = try(def.ipv6_cidr, null)
          public      = try(def.public, false)
          nat_gateway = try(def.nat_gateway, false)
          nat_subnet  = try(def.nat_subnet, null)
          shards      = try(def.shards, [])
        }
      }
    ]...)
  ]...)

  # Filter to only subnets that need creation (have ipv4_cidr, not existing)
  # Compute effective IPv6 CIDR from ipv6_netnum if set, otherwise use explicit ipv6_cidr
  subnets_to_create = {
    for k, v in local.subnet_definitions : k => merge(v, {
      ipv6_cidr = coalesce(
        v.ipv6_cidr,
        v.ipv6_netnum != null && local.vpc_ipv6_cidr != null ? cidrsubnet(local.vpc_ipv6_cidr, 16, v.ipv6_netnum) : null
      )
    }) if !v.existing
  }

  # Filter to existing subnets (have existing field)
  existing_subnets = { for k, v in local.subnet_definitions : k => v if v.existing }

  # Subnets with public = true (for external access, used for reference)
  public_subnets = { for k, v in local.subnet_definitions : k => v if v.public }

  # Subnets with nat_gateway = true (triggers Cloud NAT creation)
  # Note: GCP Cloud NAT is regional, so we only need one per region
  nat_gateway_subnets = { for k, v in local.subnet_definitions : k => v if v.nat_gateway }
  has_nat_gateway     = length(local.nat_gateway_subnets) > 0

  # For each role that has nat_gateway=true, map zone -> logical subnet ID
  nat_gateway_by_role_zone = {
    for k, v in local.nat_gateway_subnets : "${v.role}/${v.zone}" => k
  }

  # Subnets with nat_subnet set (need Cloud NAT routing)
  nat_routed_subnets = { for k, v in local.subnet_definitions : k => v if v.nat_subnet != null }

  # Map of all subnet names: key -> subnet_name
  all_subnet_names = merge(
    { for k, v in local.subnets_to_create : k => google_compute_subnetwork.managed[k].name },
    { for k, v in local.existing_subnets : k => v.name }
  )

  # Public subnet names by zone (for reference, e.g., load balancer placement)
  public_subnet_names_by_zone = {
    for k, v in local.public_subnets : v.zone => local.all_subnet_names[k]...
  }
  public_subnet_names = { for zone, names in local.public_subnet_names_by_zone : zone => names[0] }

  # Collect all unique shard IDs mentioned in any subnet definition
  all_shard_refs = distinct(flatten([
    for k, v in local.subnet_definitions : v.shards
  ]))

  # Validate shard references if cluster.shards is specified
  invalid_shards = length(local.shards) > 0 ? [
    for s in local.all_shard_refs : s if !contains(local.shards, s)
  ] : []

  # Build subnets output: role -> zone -> list of {id, shards, public}
  # Note: GCP uses subnet names as identifiers
  subnets_output = {
    for role, zones in var.subnets : role => {
      for zone, defs in zones : zone => [
        for idx, def in defs : {
          id     = local.all_subnet_names["${role}/${zone}/${idx}"]
          shards = try(def.shards, [])
          public = try(def.public, false)
        }
      ]
    }
  }

  # Subnet names that should be routed through NAT
  # Used for Cloud NAT configuration when not using ALL_SUBNETWORKS
  nat_routed_subnet_names = [
    for k, v in local.nat_routed_subnets : local.all_subnet_names[k]
  ]
}

# Validation: ipv4_cidr and existing are mutually exclusive
resource "terraform_data" "validate_cidr_existing_exclusive" {
  for_each = local.subnet_definitions

  lifecycle {
    precondition {
      condition     = !(each.value.ipv4_cidr != null && each.value.existing)
      error_message = "Subnet ${each.key}: ipv4_cidr and existing are mutually exclusive. Specify one or the other."
    }
    precondition {
      condition     = each.value.ipv4_cidr != null || each.value.existing
      error_message = "Subnet ${each.key}: must specify either ipv4_cidr or existing."
    }
  }
}

# Validation: nat_gateway = true requires public = true
resource "terraform_data" "validate_nat_gateway_public" {
  for_each = local.nat_gateway_subnets

  lifecycle {
    precondition {
      condition     = each.value.public
      error_message = "Subnet ${each.key}: nat_gateway = true requires public = true."
    }
  }
}

# Validation: nat_subnet must reference a role with nat_gateway in same zone
resource "terraform_data" "validate_nat_subnet_ref" {
  for_each = local.nat_routed_subnets

  lifecycle {
    precondition {
      condition     = contains(keys(local.nat_gateway_by_role_zone), "${each.value.nat_subnet}/${each.value.zone}")
      error_message = "Subnet ${each.key}: nat_subnet = \"${each.value.nat_subnet}\" but no subnet in role \"${each.value.nat_subnet}\" has nat_gateway = true in zone ${each.value.zone}."
    }
  }
}

resource "terraform_data" "validate_shards" {
  count = length(local.invalid_shards) > 0 ? 1 : 0

  lifecycle {
    precondition {
      condition     = length(local.invalid_shards) == 0
      error_message = "Invalid shard IDs in subnet shards filters: ${jsonencode(local.invalid_shards)}. Valid shards are: ${jsonencode(local.shards)}"
    }
  }
}

# Validation: enable_ipv6 requires ipv6_netnum or ipv6_cidr on managed subnets
resource "terraform_data" "validate_ipv6_cidrs" {
  for_each = var.enable_ipv6 && !local.use_existing_vpc ? local.subnets_to_create : {}

  lifecycle {
    precondition {
      condition     = each.value.ipv6_cidr != null
      error_message = "Subnet ${each.key}: enable_ipv6 is true but no ipv6_netnum or ipv6_cidr specified. Either set ipv6_netnum (0-65535), ipv6_cidr, or set enable_ipv6 = false."
    }
  }
}

# Managed subnets created from ipv4_cidr definitions
resource "google_compute_subnetwork" "managed" {
  for_each = local.subnets_to_create

  name          = "${local.name_prefix}-${each.value.role}-${replace(each.value.zone, "/", "-")}-${each.value.index}"
  project       = local.project_id
  ip_cidr_range = each.value.ipv4_cidr
  region        = local.region
  network       = local.vpc_id

  private_ip_google_access = true
  stack_type               = var.enable_ipv6 && !local.use_existing_vpc ? "IPV4_IPV6" : "IPV4_ONLY"
  ipv6_access_type         = var.enable_ipv6 && !local.use_existing_vpc ? "INTERNAL" : null

  # Public subnets can have different purpose if needed
  purpose = each.value.public ? "PRIVATE" : "PRIVATE"
}

# Cloud Router (required for Cloud NAT) - created when any subnet has nat_gateway = true
resource "google_compute_router" "main" {
  count = local.use_existing_vpc ? 0 : (local.has_nat_gateway ? 1 : 0)

  name    = "${local.name_prefix}-router"
  project = local.project_id
  region  = local.region
  network = local.vpc_id
}

# Cloud NAT (provides outbound internet access) - created when any subnet has nat_gateway = true
# Note: GCP Cloud NAT is regional, unlike AWS which is per-AZ
resource "google_compute_router_nat" "main" {
  count = local.use_existing_vpc ? 0 : (local.has_nat_gateway ? 1 : 0)

  name                               = "${local.name_prefix}-nat"
  project                            = local.project_id
  router                             = google_compute_router.main[0].name
  region                             = local.region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"

  log_config {
    enable = true
    filter = "ERRORS_ONLY"
  }

  depends_on = [google_compute_subnetwork.managed]
}

# Firewall rule to allow internal traffic within VPC
resource "google_compute_firewall" "allow_internal" {
  count = local.use_existing_vpc ? 0 : 1

  name    = "${local.name_prefix}-allow-internal"
  project = local.project_id
  network = local.vpc_id

  allow {
    protocol = "icmp"
  }

  allow {
    protocol = "tcp"
    ports    = ["0-65535"]
  }

  allow {
    protocol = "udp"
    ports    = ["0-65535"]
  }

  source_ranges = [var.vpc_cidr_ipv4]
}

# Firewall rule to allow health checks from Google
resource "google_compute_firewall" "allow_health_checks" {
  count = local.use_existing_vpc ? 0 : 1

  name    = "${local.name_prefix}-allow-health-checks"
  project = local.project_id
  network = local.vpc_id

  allow {
    protocol = "tcp"
  }

  # Google health check IP ranges
  source_ranges = ["35.191.0.0/16", "130.211.0.0/22"]
}

# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# ============================================================================
# Load Balancer Configuration Locals
# ============================================================================

locals {
  # Flatten load balancers into per-port entries for health checks and forwarding rules
  lb_ports = merge([
    for lb_key, lb in var.load_balancers : {
      for port in lb.ports : "${lb_key}:${port}" => {
        lb_key = lb_key
        port   = port
        public = lb.public
      }
    }
  ]...)

  # Get zones for each LB based on its subnet role
  lb_zones_by_role = {
    for role in distinct([for k, v in local.subnet_definitions : v.role]) : role =>
    distinct([for k, v in local.subnet_definitions : v.zone if v.role == role])
  }

  # Create instance group entries: one per lb_key per zone in that LB's subnet role
  lb_instance_groups = merge([
    for lb_key, lb in var.load_balancers : {
      for zone in local.lb_zones_by_role[lb.subnets] : "${lb_key}:${zone}" => {
        lb_key = lb_key
        zone   = zone
        ports  = lb.ports
      }
    }
  ]...)
}

# ============================================================================
# Static IPs (one per logical LB)
# ============================================================================

resource "google_compute_address" "lb" {
  for_each = var.load_balancers

  name   = "${var.name_prefix}-${each.key}-ip"
  region = local.region
}

# ============================================================================
# Health Checks (one per LB + port combination)
# ============================================================================

resource "google_compute_region_health_check" "nstance" {
  for_each = local.lb_ports

  name   = "${var.name_prefix}-${each.value.lb_key}-${each.value.port}-health"
  region = local.region

  tcp_health_check {
    port = each.value.port
  }

  check_interval_sec  = 10
  timeout_sec         = 5
  healthy_threshold   = 2
  unhealthy_threshold = 3
}

# ============================================================================
# Unmanaged Instance Groups (one per LB per zone)
# Instances are added/removed by nstance-server
# ============================================================================

resource "google_compute_instance_group" "nstance" {
  for_each = local.lb_instance_groups

  name    = "${var.name_prefix}-${each.value.lb_key}-ig-${each.value.zone}"
  zone    = each.value.zone
  network = local.vpc_id

  # Add named ports for all ports in this LB
  dynamic "named_port" {
    for_each = each.value.ports
    content {
      name = "port-${named_port.value}"
      port = named_port.value
    }
  }
}

# ============================================================================
# Backend Services (one per LB + port combination)
# ============================================================================

resource "google_compute_region_backend_service" "nstance" {
  for_each = local.lb_ports

  name                  = "${var.name_prefix}-${each.value.lb_key}-${each.value.port}-backend"
  region                = local.region
  protocol              = "TCP"
  load_balancing_scheme = each.value.public ? "EXTERNAL" : "INTERNAL"
  health_checks         = [google_compute_region_health_check.nstance[each.key].id]

  # Add all instance groups for this LB across zones
  dynamic "backend" {
    for_each = [for ig_key, ig in local.lb_instance_groups : ig_key if ig.lb_key == each.value.lb_key]
    content {
      group = google_compute_instance_group.nstance[backend.value].self_link
    }
  }
}

# ============================================================================
# Forwarding Rules (one per LB + port combination)
# ============================================================================

resource "google_compute_forwarding_rule" "nstance" {
  for_each = local.lb_ports

  name                  = "${var.name_prefix}-${each.value.lb_key}-${each.value.port}-fwd"
  region                = local.region
  load_balancing_scheme = each.value.public ? "EXTERNAL" : "INTERNAL"
  port_range            = each.value.port
  ip_protocol           = "TCP"
  ip_address            = google_compute_address.lb[each.value.lb_key].address
  backend_service       = google_compute_region_backend_service.nstance[each.key].id
}

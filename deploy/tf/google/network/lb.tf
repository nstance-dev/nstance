# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

# ============================================================================
# Load Balancer Configuration Locals
# ============================================================================

locals {
  lb_ports = merge([
    for lb_key, lb in var.load_balancers : {
      for listener in lb.listeners : "${lb_key}:${listener.port}" => {
        lb_key        = lb_key
        listener_port = listener.port
        target_port   = coalesce(listener.target_port, listener.port)
        proxy_port    = coalesce(listener.proxy_port, listener.target_port, listener.port)
        public        = lb.public
        subnet_role   = lb.subnets
      }
    }
  ]...)

  # A GCE_VM_IP NEG is scoped to one subnet and zone. Keep every eligible
  # subnet so independently managed instances can register by VM and IP.
  lb_network_endpoint_groups = merge([
    for lb_key, lb in var.load_balancers : {
      for subnet_key, subnet in local.subnet_definitions : "${lb_key}:${subnet_key}" => {
        lb_key      = lb_key
        subnet_key  = subnet_key
        zone        = subnet.zone
        subnet_name = local.all_subnet_names[subnet_key]
      } if contains(distinct([coalesce(lb.backend_subnets, lb.subnets), coalesce(lb.proxy_subnets, lb.subnets)]), subnet.role)
    }
  ]...)

  lb_target_tags = {
    for lb_key in keys(var.load_balancers) : lb_key => flatten([
      for shard in local.shards : [
        "nstance-lb-${substr(md5(lb_key), 0, 12)}-${substr(md5(shard), 0, 12)}-agent",
        "nstance-lb-${substr(md5(lb_key), 0, 12)}-${substr(md5(shard), 0, 12)}-server"
      ]
    ])
  }
}

resource "terraform_data" "validate_load_balancer_subnets" {
  for_each = var.load_balancers

  lifecycle {
    precondition {
      condition     = length(local.shards) > 0
      error_message = "Google Cloud load balancers require cluster.shards for firewall targeting."
    }
    precondition {
      condition     = anytrue([for subnet in values(local.subnet_definitions) : subnet.role == each.value.subnets])
      error_message = "Load balancer ${each.key} references frontend subnet role ${each.value.subnets}, which has no subnets."
    }
    precondition {
      condition     = anytrue([for subnet in values(local.subnet_definitions) : subnet.role == coalesce(each.value.backend_subnets, each.value.subnets)])
      error_message = "Load balancer ${each.key} references a backend subnet role which has no subnets."
    }
    precondition {
      condition     = anytrue([for subnet in values(local.subnet_definitions) : subnet.role == coalesce(each.value.proxy_subnets, each.value.subnets)])
      error_message = "Load balancer ${each.key} references a proxy subnet role which has no subnets."
    }
    precondition {
      condition = alltrue([
        for listener in each.value.listeners :
        coalesce(listener.target_port, listener.port) == listener.port &&
        coalesce(listener.proxy_port, listener.target_port, listener.port) == listener.port
      ])
      error_message = "Google Cloud passthrough load balancers require listener, target, and proxy ports to match."
    }
  }
}

# ============================================================================
# Zonal Network Endpoint Groups
# ============================================================================

resource "google_compute_network_endpoint_group" "nstance" {
  for_each = local.lb_network_endpoint_groups

  name                  = "${local.name_prefix}-${each.value.lb_key}-neg-${replace(each.value.zone, "/", "-")}-${substr(md5(each.value.subnet_key), 0, 6)}"
  project               = local.project_id
  zone                  = each.value.zone
  network               = local.vpc_id
  subnetwork            = each.value.subnet_name
  network_endpoint_type = "GCE_VM_IP"
}

# ============================================================================
# Frontend Addresses
# ============================================================================

resource "google_compute_address" "lb_external" {
  for_each = { for key, lb in var.load_balancers : key => lb if lb.public }

  name         = "${local.name_prefix}-${each.key}-ip"
  project      = local.project_id
  region       = local.region
  address_type = "EXTERNAL"
  network_tier = "PREMIUM"
}

resource "google_compute_address" "lb_internal" {
  for_each = { for key, lb in var.load_balancers : key => lb if !lb.public }

  name         = "${local.name_prefix}-${each.key}-ip"
  project      = local.project_id
  region       = local.region
  address_type = "INTERNAL"
  subnetwork   = local.all_subnet_names[[for key, subnet in local.subnet_definitions : key if subnet.role == each.value.subnets][0]]
  purpose      = "SHARED_LOADBALANCER_VIP"
}

# ============================================================================
# Health Checks and Regional Passthrough Backend Services
# ============================================================================

resource "google_compute_region_health_check" "nstance" {
  for_each = local.lb_ports

  name    = "${local.name_prefix}-${each.value.lb_key}-${each.value.listener_port}-health"
  project = local.project_id
  region  = local.region

  tcp_health_check {
    port = each.value.target_port
  }

  check_interval_sec  = 10
  timeout_sec         = 5
  healthy_threshold   = 2
  unhealthy_threshold = 3
}

resource "google_compute_region_backend_service" "nstance" {
  for_each = local.lb_ports

  name                  = "${local.name_prefix}-${each.value.lb_key}-${each.value.listener_port}-backend"
  project               = local.project_id
  region                = local.region
  protocol              = "TCP"
  load_balancing_scheme = each.value.public ? "EXTERNAL" : "INTERNAL"
  health_checks         = [google_compute_region_health_check.nstance[each.key].id]

  dynamic "backend" {
    for_each = { for key, neg in local.lb_network_endpoint_groups : key => neg if neg.lb_key == each.value.lb_key }
    content {
      group = google_compute_network_endpoint_group.nstance[backend.key].self_link
    }
  }
}

# ============================================================================
# Regional Passthrough Forwarding Rules
# ============================================================================

resource "google_compute_forwarding_rule" "nstance" {
  for_each = local.lb_ports

  name                  = "${local.name_prefix}-${each.value.lb_key}-${each.value.listener_port}-fwd"
  project               = local.project_id
  region                = local.region
  load_balancing_scheme = each.value.public ? "EXTERNAL" : "INTERNAL"
  ports                 = [each.value.listener_port]
  ip_protocol           = "TCP"
  ip_address            = each.value.public ? google_compute_address.lb_external[each.value.lb_key].address : google_compute_address.lb_internal[each.value.lb_key].address
  backend_service       = google_compute_region_backend_service.nstance[each.key].id
  subnetwork            = each.value.public ? null : local.all_subnet_names[[for key, subnet in local.subnet_definitions : key if subnet.role == each.value.subnet_role][0]]
  network_tier          = each.value.public ? "PREMIUM" : null

  depends_on = [terraform_data.validate_load_balancer_subnets]
}

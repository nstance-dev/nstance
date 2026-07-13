# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

# ============================================================================
# Load Balancer Configuration Locals
# ============================================================================

locals {
  # Flatten load balancers into per-listener entries for target groups and listeners
  lb_ports = merge([
    for lb_key, lb in var.load_balancers : {
      for listener in lb.listeners : "${lb_key}:${listener.port}" => {
        lb_key        = lb_key
        listener_port = listener.port
        target_port   = coalesce(listener.target_port, listener.port)
      }
    }
  ]...)

  # AWS load balancer names have a 32-character limit. Preserve readability
  # while appending a hash when truncation is required to avoid collisions.
  lb_names = {
    for lb_key in keys(var.load_balancers) : lb_key => (
      length("${local.name_prefix}-${lb_key}") <= 32
      ? "${local.name_prefix}-${lb_key}"
      : "${substr("${local.name_prefix}-${lb_key}", 0, 23)}-${substr(sha1("${local.name_prefix}-${lb_key}"), 0, 8)}"
    )
  }

  # AWS target group names also have a 32-character limit; use the same
  # collision-resistant truncation pattern as load balancer names.
  target_group_names = {
    for lb_port_key, lb_port in local.lb_ports : lb_port_key => (
      length("${local.name_prefix}-${lb_port.lb_key}-${lb_port.listener_port}-${lb_port.target_port}") <= 32
      ? "${local.name_prefix}-${lb_port.lb_key}-${lb_port.listener_port}-${lb_port.target_port}"
      : "${substr("${local.name_prefix}-${lb_port.lb_key}-${lb_port.listener_port}-${lb_port.target_port}", 0, 23)}-${substr(sha1("${local.name_prefix}-${lb_port.lb_key}-${lb_port.listener_port}-${lb_port.target_port}"), 0, 8)}"
    )
  }

  # Build subnet IDs per role (first subnet per zone in each role)
  subnets_by_role = {
    for role in distinct([for k, v in local.subnet_definitions : v.role]) : role => distinct([
      for k, v in local.subnet_definitions : local.all_subnet_ids[k] if v.role == role
    ])
  }

  # Check if a role has any public subnets
  role_has_public_subnets = {
    for role in distinct([for k, v in local.subnet_definitions : v.role]) : role =>
    anytrue([for k, v in local.subnet_definitions : v.public if v.role == role])
  }

  lb_ipv4_ingress = merge([
    for lb_port_key, lb_port in local.lb_ports : {
      for cidr in var.load_balancers[lb_port.lb_key].public ? ["0.0.0.0/0"] : local.vpc_cidr_blocks :
      "${lb_port_key}:${cidr}" => merge(lb_port, { cidr = cidr })
    }
  ]...)
}

# ============================================================================
# Validation: public LBs require public subnets
# ============================================================================

resource "terraform_data" "validate_public_lb_subnets" {
  for_each = { for k, v in var.load_balancers : k => v if v.public }

  lifecycle {
    precondition {
      condition     = local.role_has_public_subnets[each.value.subnets]
      error_message = "Load balancer \"${each.key}\": public = true requires subnets with public = true. Subnet role \"${each.value.subnets}\" has no public subnets."
    }
  }
}

# ============================================================================
# Network Load Balancers (one per logical LB)
# ============================================================================

resource "aws_lb" "nstance" {
  for_each = var.load_balancers

  name               = local.lb_names[each.key]
  internal           = !each.value.public
  load_balancer_type = "network"
  ip_address_type    = var.enable_ipv6 && !local.use_existing_vpc ? "dualstack" : "ipv4"
  subnets            = local.subnets_by_role[each.value.subnets]
  security_groups    = [aws_security_group.load_balancer[each.key].id]

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-${each.key}"
  })
}

# Security groups must be attached when NLBs are created. Public listeners are
# open to clients; private listeners are restricted to the VPC.
resource "aws_security_group" "load_balancer" {
  for_each = var.load_balancers

  name        = "${local.lb_names[each.key]}-sg"
  description = "Security group for ${local.lb_names[each.key]}"
  vpc_id      = local.vpc_id

  tags = merge(var.tags, {
    Name = "${local.lb_names[each.key]}-sg"
  })
}

resource "aws_vpc_security_group_ingress_rule" "load_balancer" {
  for_each = local.lb_ipv4_ingress

  security_group_id = aws_security_group.load_balancer[each.value.lb_key].id
  description       = "Load balancer listener ${each.value.listener_port}"
  from_port         = each.value.listener_port
  to_port           = each.value.listener_port
  ip_protocol       = "tcp"
  cidr_ipv4         = each.value.cidr
}

resource "aws_vpc_security_group_ingress_rule" "load_balancer_ipv6" {
  for_each = var.enable_ipv6 && !local.use_existing_vpc ? local.lb_ports : {}

  security_group_id = aws_security_group.load_balancer[each.value.lb_key].id
  description       = "Load balancer listener ${each.value.listener_port} (IPv6)"
  from_port         = each.value.listener_port
  to_port           = each.value.listener_port
  ip_protocol       = "tcp"
  cidr_ipv6         = var.load_balancers[each.value.lb_key].public ? "::/0" : local.vpc_ipv6_cidr
}

resource "aws_vpc_security_group_egress_rule" "load_balancer" {
  for_each = var.load_balancers

  security_group_id = aws_security_group.load_balancer[each.key].id
  description       = "Load balancer traffic to targets"
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "load_balancer_ipv6" {
  for_each = var.enable_ipv6 && !local.use_existing_vpc ? var.load_balancers : {}

  security_group_id = aws_security_group.load_balancer[each.key].id
  description       = "Load balancer traffic to targets (IPv6)"
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}

# ============================================================================
# Target Groups (one per LB + port combination)
# ============================================================================

resource "aws_lb_target_group" "nstance" {
  for_each = local.lb_ports

  name        = local.target_group_names[each.key]
  port        = each.value.target_port
  protocol    = "TCP"
  vpc_id      = local.vpc_id
  target_type = "instance"

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-${each.value.lb_key}-${each.value.listener_port}"
  })
}

# ============================================================================
# Listeners (one per LB + port combination)
# ============================================================================

resource "aws_lb_listener" "nstance" {
  for_each = local.lb_ports

  load_balancer_arn = aws_lb.nstance[each.value.lb_key].arn
  port              = each.value.listener_port
  protocol          = "TCP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.nstance[each.key].arn
  }
}

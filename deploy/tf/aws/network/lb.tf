# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# ============================================================================
# Load Balancer Configuration Locals
# ============================================================================

locals {
  # Flatten load balancers into per-port entries for target groups and listeners
  lb_ports = merge([
    for lb_key, lb in var.load_balancers : {
      for port in lb.ports : "${lb_key}:${port}" => {
        lb_key = lb_key
        port   = port
      }
    }
  ]...)

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

  name               = "${var.name_prefix}-${each.key}"
  internal           = !each.value.public
  load_balancer_type = "network"
  subnets            = local.subnets_by_role[each.value.subnets]

  tags = merge(var.tags, {
    Name = "${var.name_prefix}-${each.key}"
  })
}

# ============================================================================
# Target Groups (one per LB + port combination)
# ============================================================================

resource "aws_lb_target_group" "nstance" {
  for_each = local.lb_ports

  # AWS target group names have a 32-char limit
  name        = substr("${var.name_prefix}-${each.value.lb_key}-${each.value.port}", 0, 32)
  port        = each.value.port
  protocol    = "TCP"
  vpc_id      = local.vpc_id
  target_type = "instance"

  tags = merge(var.tags, {
    Name = "${var.name_prefix}-${each.value.lb_key}-${each.value.port}"
  })
}

# ============================================================================
# Listeners (one per LB + port combination)
# ============================================================================

resource "aws_lb_listener" "nstance" {
  for_each = local.lb_ports

  load_balancer_arn = aws_lb.nstance[each.value.lb_key].arn
  port              = each.value.port
  protocol          = "TCP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.nstance[each.key].arn
  }
}

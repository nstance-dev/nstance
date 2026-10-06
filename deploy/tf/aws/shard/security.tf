# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

# Server security group
resource "aws_security_group" "server" {
  name        = "${local.name_prefix}-server-sg-${var.shard}"
  description = "Security group for Nstance Server (${var.shard})"
  vpc_id      = var.network.vpc_id

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-server-sg-${var.shard}"
  })
}

# Health check endpoint
resource "aws_vpc_security_group_ingress_rule" "server_health" {
  security_group_id = aws_security_group.server.id
  description       = "Health check from VPC"
  from_port         = local.health_port
  to_port           = local.health_port
  ip_protocol       = "tcp"
  cidr_ipv4         = var.network.vpc_cidr_ipv4
}

resource "aws_vpc_security_group_ingress_rule" "server_health_ipv6" {
  count = var.network.ipv6_enabled ? 1 : 0

  security_group_id = aws_security_group.server.id
  description       = "Health check from VPC (IPv6)"
  from_port         = local.health_port
  to_port           = local.health_port
  ip_protocol       = "tcp"
  cidr_ipv6         = var.network.vpc_cidr_ipv6
}

# gRPC APIs (election through agent ports)
resource "aws_vpc_security_group_ingress_rule" "server_grpc" {
  security_group_id = aws_security_group.server.id
  description       = "gRPC from VPC"
  from_port         = local.election_port
  to_port           = local.agent_port
  ip_protocol       = "tcp"
  cidr_ipv4         = var.network.vpc_cidr_ipv4
}

resource "aws_vpc_security_group_ingress_rule" "server_grpc_ipv6" {
  count = var.network.ipv6_enabled ? 1 : 0

  security_group_id = aws_security_group.server.id
  description       = "gRPC from VPC (IPv6)"
  from_port         = local.election_port
  to_port           = local.agent_port
  ip_protocol       = "tcp"
  cidr_ipv6         = var.network.vpc_cidr_ipv6
}

# All outbound
resource "aws_vpc_security_group_egress_rule" "server_all" {
  security_group_id = aws_security_group.server.id
  description       = "All outbound"
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "server_all_ipv6" {
  security_group_id = aws_security_group.server.id
  description       = "All outbound (IPv6)"
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}

# Agent security group
resource "aws_security_group" "agent" {
  name        = "${local.name_prefix}-agent-sg-${var.shard}"
  description = "Security group for Nstance Agent (${var.shard})"
  vpc_id      = var.network.vpc_id

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-agent-sg-${var.shard}"
  })
}

# NAT instances need a separate security group because they accept forwarded
# IPv4 traffic from the VPC; ordinary agent instances do not accept it.
resource "aws_security_group" "nat" {
  count = var.network.nat_mode == "nstance" ? 1 : 0

  name        = "${local.name_prefix}-nat-sg-${var.shard}"
  description = "Security group for Nstance NAT instances (${var.shard})"
  vpc_id      = var.network.vpc_id

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-nat-sg-${var.shard}"
  })
}

resource "aws_vpc_security_group_ingress_rule" "nat_forward_ipv4" {
  count = var.network.nat_mode == "nstance" ? 1 : 0

  security_group_id = aws_security_group.nat[0].id
  description       = "Forward IPv4 traffic from the VPC"
  ip_protocol       = "-1"
  cidr_ipv4         = var.network.vpc_cidr_ipv4
}

resource "aws_vpc_security_group_ingress_rule" "nat_forward_ipv6" {
  count = var.network.nat_mode == "nstance" && var.network.ipv6_enabled ? 1 : 0

  security_group_id = aws_security_group.nat[0].id
  description       = "Forward NAT64 traffic from the VPC"
  ip_protocol       = "-1"
  cidr_ipv6         = var.network.vpc_cidr_ipv6
}

resource "aws_vpc_security_group_egress_rule" "nat_all_ipv4" {
  count = var.network.nat_mode == "nstance" ? 1 : 0

  security_group_id = aws_security_group.nat[0].id
  description       = "All outbound IPv4"
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "nat_all_ipv6" {
  count = var.network.nat_mode == "nstance" && var.network.ipv6_enabled ? 1 : 0

  security_group_id = aws_security_group.nat[0].id
  description       = "All outbound IPv6"
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}

locals {
  load_balancer_target_ports = merge([
    for lb_key, lb in var.network.load_balancers : {
      for target_group in lb.target_groups : "${lb_key}:${target_group.listener_port}" => {
        security_group_id = lb.security_group_id
        target_port       = target_group.target_port
      }
    }
  ]...)

  load_balancer_proxy_ports = merge([
    for lb_key, lb in var.network.load_balancers : {
      for target_group in lb.target_groups : "${lb_key}:${target_group.listener_port}" => {
        security_group_id = lb.security_group_id
        proxy_port        = target_group.proxy_port
      }
    }
  ]...)
}

# Permit only traffic forwarded by an attached NLB security group.
resource "aws_vpc_security_group_ingress_rule" "agent_load_balancer" {
  for_each = local.load_balancer_target_ports

  security_group_id            = aws_security_group.agent.id
  description                  = "Load balancer traffic on target port ${each.value.target_port}"
  from_port                    = each.value.target_port
  to_port                      = each.value.target_port
  ip_protocol                  = "tcp"
  referenced_security_group_id = each.value.security_group_id
}

resource "aws_vpc_security_group_ingress_rule" "server_load_balancer_proxy" {
  for_each = local.load_balancer_proxy_ports

  security_group_id            = aws_security_group.server.id
  description                  = "Load balancer proxy traffic on port ${each.value.proxy_port}"
  from_port                    = each.value.proxy_port
  to_port                      = each.value.proxy_port
  ip_protocol                  = "tcp"
  referenced_security_group_id = each.value.security_group_id
}

# All outbound for agents
resource "aws_vpc_security_group_egress_rule" "agent_all" {
  security_group_id = aws_security_group.agent.id
  description       = "All outbound"
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "agent_all_ipv6" {
  security_group_id = aws_security_group.agent.id
  description       = "All outbound (IPv6)"
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}

# SSH access (conditional)
resource "aws_vpc_security_group_ingress_rule" "server_ssh" {
  count = var.ssh_key_name != "" ? 1 : 0

  security_group_id = aws_security_group.server.id
  description       = "SSH from VPC"
  from_port         = 22
  to_port           = 22
  ip_protocol       = "tcp"
  cidr_ipv4         = var.network.vpc_cidr_ipv4
}

resource "aws_vpc_security_group_ingress_rule" "agent_ssh" {
  count = var.ssh_key_name != "" ? 1 : 0

  security_group_id = aws_security_group.agent.id
  description       = "SSH from VPC"
  from_port         = 22
  to_port           = 22
  ip_protocol       = "tcp"
  cidr_ipv4         = var.network.vpc_cidr_ipv4
}

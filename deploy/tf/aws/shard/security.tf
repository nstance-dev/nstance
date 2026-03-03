# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
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
  count = var.network.enable_ipv6 ? 1 : 0

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
  count = var.network.enable_ipv6 ? 1 : 0

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

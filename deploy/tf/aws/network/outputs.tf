# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

output "vpc_id" {
  description = "VPC ID"
  value       = local.vpc_id
}

output "vpc_cidr_ipv4" {
  description = "VPC IPv4 CIDR block"
  value       = local.vpc_cidr_block
}

output "vpc_cidr_ipv6" {
  description = "VPC IPv6 CIDR block (null if IPv6 disabled or using existing VPC)"
  value       = local.vpc_ipv6_cidr
}

output "enable_ipv6" {
  description = "Whether IPv6 is enabled (known at plan time, use for count/for_each)"
  value       = var.enable_ipv6 && !local.use_existing_vpc
}

output "public_route_table_id" {
  description = "Public route table ID (null when using existing VPC)"
  value       = local.use_existing_vpc ? null : aws_route_table.public[0].id
}

output "private_route_table_ids" {
  description = "Map of AZ -> private route table ID"
  value       = local.private_route_table_ids_by_az
}

output "nat_gateway_ids" {
  description = "Map of AZ -> NAT gateway ID"
  value       = local.nat_gateway_ids_by_az
}

output "public_subnet_ids" {
  description = "Map of AZ -> public subnet ID (for NLB placement)"
  value       = local.public_subnet_ids
}

output "subnet_ids" {
  description = "Map of all subnet IDs by key (role/zone/index)"
  value       = local.all_subnet_ids
}

output "subnets" {
  description = "Subnet metadata by role and zone. Structure: role -> zone -> list of {id, shards, public}."
  value       = local.subnets_output
}

output "load_balancers" {
  description = "Map of load balancer configurations with target group ARNs"
  value = {
    for lb_key, lb in var.load_balancers : lb_key => {
      dns_name          = aws_lb.nstance[lb_key].dns_name
      arn               = aws_lb.nstance[lb_key].arn
      target_group_arns = [for port in lb.ports : aws_lb_target_group.nstance["${lb_key}:${port}"].arn]
    }
  }
}

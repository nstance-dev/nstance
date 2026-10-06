# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
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

output "ipv4_enabled" {
  description = "Whether IPv4 is enabled for workload subnets"
  value       = var.ipv4_enabled
}

output "ipv6_enabled" {
  description = "Whether IPv6 is enabled (known at plan time, use for count/for_each)"
  value       = var.ipv6_enabled && !local.use_existing_vpc
}

output "public_route_table_id" {
  description = "Public route table ID (null when using existing VPC)"
  value       = local.use_existing_vpc ? null : aws_route_table.public[0].id
}

output "private_route_table_ids" {
  description = "Map of subnet role/zone/index keys to private route table IDs"
  value       = local.private_route_table_ids
}

output "nat_gateway_ids" {
  description = "Map of AZ -> NAT gateway ID"
  value       = local.nat_gateway_ids_by_az
}

output "nat_public_addresses" {
  description = "Optional fixed public IPv4 attachments for Nstance NAT instances, keyed by service role and zone"
  value = {
    for group in distinct([for address in values(local.fixed_public_ipv4) : "${address.role}-${address.zone}"]) : group => [
      for key, address in local.fixed_public_ipv4 : {
        ipv4          = aws_eip.managed_nat[key].public_ip
        allocation_id = aws_eip.managed_nat[key].allocation_id
      } if "${address.role}-${address.zone}" == group
    ]
  }
}

output "nat_mode" {
  description = "NAT implementation: none, provider, or nstance"
  value       = var.nat_mode
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
      zone_id           = aws_lb.nstance[lb_key].zone_id
      security_group_id = aws_security_group.load_balancer[lb_key].id
      target_ports      = distinct([for listener in lb.listeners : coalesce(listener.target_port, listener.port)])
      target_groups = [for listener in lb.listeners : {
        arn           = aws_lb_target_group.nstance["${lb_key}:${listener.port}"].arn
        listener_port = listener.port
        target_port   = coalesce(listener.target_port, listener.port)
        proxy_port    = coalesce(listener.proxy_port, listener.target_port, listener.port)
      }]
    }
  }
}

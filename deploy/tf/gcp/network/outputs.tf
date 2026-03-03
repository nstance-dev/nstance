# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

output "vpc_id" {
  description = "VPC network self_link"
  value       = local.vpc_id
}

output "vpc_cidr" {
  description = "VPC IPv4 CIDR block (reference value, GCP manages per-subnet)"
  value       = var.vpc_cidr_ipv4
}

output "vpc_cidr_ipv4" {
  description = "VPC IPv4 CIDR block (alias for vpc_cidr)"
  value       = var.vpc_cidr_ipv4
}

output "vpc_cidr_ipv6" {
  description = "VPC internal IPv6 CIDR range (null if IPv6 disabled or using existing VPC)"
  value       = local.vpc_ipv6_cidr
}

output "public_subnet_names" {
  description = "Map of zone -> public subnet name (for load balancer placement)"
  value       = local.public_subnet_names
}

output "nat_gateway_name" {
  description = "Cloud NAT name (null when using existing VPC or no NAT configured)"
  value       = local.use_existing_vpc ? null : (local.has_nat_gateway ? google_compute_router_nat.main[0].name : null)
}

output "router_name" {
  description = "Cloud Router name (null when using existing VPC or no NAT configured)"
  value       = local.use_existing_vpc ? null : (local.has_nat_gateway ? google_compute_router.main[0].name : null)
}

output "subnet_names" {
  description = "Map of all subnet names by key (role/zone/index)"
  value       = local.all_subnet_names
}

output "subnets" {
  description = "Subnet metadata by role and zone. Structure: role -> zone -> list of {id, shards, public}. Note: id is the subnet name for GCP."
  value       = local.subnets_output
}

output "load_balancers" {
  description = "Map of load balancer configurations with instance group info"
  value = {
    for lb_key, lb in var.load_balancers : lb_key => {
      ip_address = google_compute_address.lb[lb_key].address
      instance_groups = {
        for ig_key, ig in local.lb_instance_groups : ig.zone => google_compute_instance_group.nstance[ig_key].name
        if ig.lb_key == lb_key
      }
    }
  }
}

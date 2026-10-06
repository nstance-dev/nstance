# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

output "vpc_id" {
  description = "VPC network self_link"
  value       = local.vpc_id
}

output "vpc_cidr" {
  description = "VPC IPv4 CIDR block (reference value, Google Cloud manages per-subnet)"
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
  description = "Cloud NAT name (null when no NAT is configured)"
  value       = local.has_nat_gateway && var.use_provider_nat ? google_compute_router_nat.main[0].name : null
}

output "router_name" {
  description = "Cloud Router name (null when no NAT is configured)"
  value       = local.has_nat_gateway && var.use_provider_nat ? google_compute_router.main[0].name : null
}

output "use_provider_nat" {
  description = "Whether the cloud provider's NAT service is used instead of Nstance NAT instances"
  value       = var.use_provider_nat
}

output "public_addresses" {
  description = "Optional fixed public IPv4 addresses for Nstance NAT instances, keyed by service role and zone"
  value = {
    for group in distinct([for address in values(local.fixed_public_ipv4) : "${address.role}-${address.zone}"]) : group => [
      for key, address in local.fixed_public_ipv4 : {
        ipv4 = google_compute_address.nat_external[key].address
      } if "${address.role}-${address.zone}" == group
    ]
  }
}

output "subnet_names" {
  description = "Map of all subnet names by key (role/zone/index)"
  value       = local.all_subnet_names
}

output "subnets" {
  description = "Subnet metadata by role and zone. Structure: role -> zone -> list of {id, shards, public}. Note: id is the subnet name for Google Cloud."
  value       = local.subnets_output
}

output "load_balancers" {
  description = "Map of load balancer network endpoint groups and forwarding-rule frontends"
  value = {
    for lb_key, lb in var.load_balancers : lb_key => {
      network_endpoint_groups = {
        for zone in distinct([for neg in values(local.lb_network_endpoint_groups) : neg.zone if neg.lb_key == lb_key]) : zone => [
          for neg_key, neg in local.lb_network_endpoint_groups : google_compute_network_endpoint_group.nstance[neg_key].name
          if neg.lb_key == lb_key && neg.zone == zone
        ]
      }
      frontends = [
        for listener in lb.listeners : {
          ip   = lb.public ? google_compute_address.lb_external[lb_key].address : google_compute_address.lb_internal[lb_key].address
          port = listener.port
        }
      ]
    }
  }
}

# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0
#
# Minimal Single-Shard Deployment (AWS)
#
# This example demonstrates a minimal single-shard deployment with:
# - New VPC with public and private subnets
# - AWS NAT Gateway or Nstance NAT instances for outbound IPv4 traffic
# - Single shard with worker group

variable "profile" {
  description = "AWS CLI profile name"
  type        = string
}

variable "region" {
  description = "AWS region"
  type        = string
}

variable "zone" {
  description = "AWS availability zone"
  type        = string
}

variable "cluster_id" {
  description = "Cluster ID (lowercase alphanumeric with hyphens, max 32 chars)"
  type        = string
}

variable "ipv4_enabled" {
  description = "Enable IPv4 on workload subnets"
  type        = bool
  default     = true
}

variable "ipv6_enabled" {
  description = "Enable IPv6 on workload subnets"
  type        = bool
  default     = true
}

variable "nat_mode" {
  description = "NAT implementation: none, provider, or nstance"
  type        = string
  default     = "nstance"
}

variable "load_balancers" {
  description = "Optional load balancers associated with the workers group"
  type = map(object({
    listeners = list(object({
      port        = number
      target_port = optional(number)
      proxy_port  = optional(number)
    }))
    subnets         = string
    backend_subnets = optional(string)
    proxy_subnets   = optional(string)
    public          = bool
  }))
  default = {}
}

variable "nstance_server_binary_url" {
  description = "Optional fixed URL for the nstance-server binary tarball"
  type        = string
  default     = ""
}

variable "nstance_agent_binary_url" {
  description = "Optional fixed URL for the nstance-agent binary tarball"
  type        = string
  default     = ""
}

provider "aws" {
  profile = var.profile
  region  = var.region
}

module "cluster" {
  source = "../../../aws/cluster"

  aws_profile = var.profile
  cluster_id  = var.cluster_id
}

module "account" {
  source = "../../../aws/account"

  cluster = module.cluster
}

module "network" {
  source = "../../../aws/network"

  cluster        = module.cluster
  vpc_cidr_ipv4  = "172.18.0.0/16"
  ipv4_enabled   = var.ipv4_enabled
  ipv6_enabled   = var.ipv6_enabled
  nat_mode       = var.nat_mode
  load_balancers = var.load_balancers

  # Define subnets by role and zone
  # ipv6_netnum (0-255) auto-computes /64 from VPC's AWS-assigned /56
  subnets = {
    # Public subnet with NAT gateway for outbound traffic
    "public" = {
      (var.zone) = [{
        ipv4_cidr   = "172.18.0.0/24"
        ipv6_netnum = 0
        public      = true
        nat_gateway = true
      }]
    }
    # Nstance Server subnet routes through NAT
    "nstance" = {
      (var.zone) = [{
        ipv4_cidr   = "172.18.1.0/28"
        ipv6_netnum = 1
        nat_subnet  = "public"
      }]
    }
    # Worker subnet routes through NAT
    "workers" = {
      (var.zone) = [{
        ipv4_cidr   = "172.18.10.0/24"
        ipv6_netnum = 10
        nat_subnet  = "public"
      }]
    }
  }
}

module "shard" {
  source = "../../../aws/shard"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard         = var.zone
  zone          = var.zone
  server_subnet = var.nat_mode == "nstance" ? "public" : "nstance"

  nstance_server_binary_url = var.nstance_server_binary_url
  nstance_agent_binary_url  = var.nstance_agent_binary_url

  nat = var.nat_mode == "nstance" ? {
    default = {
      group                = "nat"
      public_addresses     = try(module.network.nat_public_addresses["public-${var.zone}"], [])
      instance_type_ladder = ["t4g.nano"]
    }
  } : {}

  templates = {
    default = { kind = "dft", arch = "arm64" }
    nat     = { kind = "nat", arch = "arm64" }
  }

  groups = {
    "default" = merge({
      "workers" = {
        size           = 1
        subnet_pool    = "workers" # References key from subnets map
        load_balancers = keys(var.load_balancers)
      }
      }, var.nat_mode == "nstance" ? {
      nat = {
        subnet_pool   = "public"
        template      = "nat"
        instance_type = "t4g.nano"
      }
    } : {})
  }
}

output "server_private_ips" {
  description = "Private IP addresses of the Nstance servers"
  value       = module.shard.server_ips
}

output "config_key" {
  description = "Object-storage key containing the shard configuration"
  value       = module.shard.config_key
}

output "nat_mode" {
  description = "Configured NAT implementation"
  value       = module.network.nat_mode
}

output "nat_public_addresses" {
  description = "Fixed public IPv4 addresses allocated for Nstance NAT instances"
  value       = module.network.nat_public_addresses
}

output "load_balancer_endpoints" {
  description = "TCP frontend endpoints by load balancer name"
  value = {
    for name, lb in module.network.load_balancers : name => [
      for listener in var.load_balancers[name].listeners : {
        host = lb.dns_name
        port = listener.port
      }
    ]
  }
}

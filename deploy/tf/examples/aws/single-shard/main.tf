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

variable "use_provider_nat" {
  description = "Use AWS NAT Gateway instead of Nstance NAT instances"
  type        = bool
  default     = false
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

  cluster          = module.cluster
  vpc_cidr_ipv4    = "172.18.0.0/16"
  use_provider_nat = var.use_provider_nat

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
  server_subnet = var.use_provider_nat ? "nstance" : "public"

  nat = var.use_provider_nat ? {} : {
    default = {
      group                = "nat"
      public_addresses     = try(module.network.public_addresses["public-${var.zone}"], [])
      instance_type_ladder = ["t4g.nano"]
    }
  }

  templates = {
    default = { kind = "dft", arch = "arm64" }
    nat     = { kind = "nat", arch = "arm64" }
  }

  groups = {
    "default" = merge({
      "workers" = {
        size        = 1
        subnet_pool = "workers" # References key from subnets map
      }
      }, var.use_provider_nat ? {} : {
      nat = {
        subnet_pool   = "public"
        template      = "nat"
        instance_type = "t4g.nano"
      }
    })
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

output "use_provider_nat" {
  description = "Whether AWS NAT Gateway is used instead of Nstance NAT instances"
  value       = module.network.use_provider_nat
}

output "nat_public_addresses" {
  description = "Fixed public IPv4 addresses allocated for Nstance NAT instances"
  value       = module.network.public_addresses
}

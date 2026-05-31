# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0
#
# Minimal Single-Shard Deployment (AWS)
#
# This example demonstrates a minimal single-shard deployment with:
# - New VPC with public and private subnets
# - NAT gateway for outbound traffic
# - Single shard with worker group

variable "profile" {
  description = "AWS CLI profile name"
  type        = string
}

variable "region" {
  description = "AWS region"
  type        = string
}

variable "cluster_id" {
  description = "Cluster ID (lowercase alphanumeric with hyphens, max 32 chars)"
  type        = string
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

  cluster       = module.cluster
  vpc_cidr_ipv4 = "172.18.0.0/16"

  # Define subnets by role and zone
  # ipv6_netnum (0-255) auto-computes /64 from VPC's AWS-assigned /56
  subnets = {
    # Public subnet with NAT gateway for outbound traffic
    "public" = {
      "us-west-2a" = [{
        ipv4_cidr   = "172.18.0.0/24"
        ipv6_netnum = 0
        public      = true
        nat_gateway = true
      }]
    }
    # Nstance Server subnet routes through NAT
    "nstance" = {
      "us-west-2a" = [{
        ipv4_cidr   = "172.18.1.0/28"
        ipv6_netnum = 1
        nat_subnet  = "public"
      }]
    }
    # Worker subnet routes through NAT
    "workers" = {
      "us-west-2a" = [{
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

  shard = "us-west-2a"
  zone  = "us-west-2a"
  # server_subnet defaults to "nstance" - uses first subnet from that role in zone

  groups = {
    "default" = {
      "workers" = {
        size        = 1
        subnet_pool = "workers" # References key from subnets map
      }
    }
  }
}

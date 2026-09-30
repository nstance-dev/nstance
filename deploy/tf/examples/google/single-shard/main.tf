# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0
#
# Minimal Single-Shard Deployment (Google Cloud)
#
# This example demonstrates a minimal single-shard deployment with:
# - New VPC with public and private subnets
# - Cloud NAT for outbound traffic
# - Single shard with worker group

variable "project" {
  description = "Google Cloud project ID"
  type        = string
}

variable "region" {
  description = "Google Cloud region"
  type        = string
}

variable "cluster_id" {
  description = "Cluster ID (lowercase alphanumeric with hyphens, max 32 chars)"
  type        = string
}

variable "nat_mode" {
  description = "IPv4 egress mode"
  type        = string
  default     = "cloud-managed"
}

provider "google" {
  project = var.project
  region  = var.region
}

module "cluster" {
  source = "../../../google/cluster"

  cluster_id = var.cluster_id
}

module "account" {
  source = "../../../google/account"

  cluster = module.cluster
}

module "network" {
  source = "../../../google/network"

  cluster       = module.cluster
  vpc_cidr_ipv4 = "172.18.0.0/16"
  nat_mode      = var.nat_mode

  # Define subnets by role and zone
  # ipv6_netnum (0-65535) auto-computes /64 from VPC's Google Cloud-assigned /48
  subnets = {
    # Public subnet with Cloud NAT for outbound traffic
    "public" = {
      "us-central1-a" = [{
        ipv4_cidr   = "172.18.0.0/24"
        ipv6_netnum = 0
        public      = true
        nat_gateway = true
      }]
    }
    # Nstance Server subnet routes through NAT
    "nstance" = {
      "us-central1-a" = [{
        ipv4_cidr   = "172.18.1.0/28"
        ipv6_netnum = 1
        nat_subnet  = "public"
      }]
    }
    # Worker subnet routes through NAT
    "workers" = {
      "us-central1-a" = [{
        ipv4_cidr   = "172.18.10.0/24"
        ipv6_netnum = 10
        nat_subnet  = "public"
      }]
    }
  }
}

module "shard" {
  source = "../../../google/shard"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard         = "us-central1-a"
  zone          = "us-central1-a"
  server_subnet = var.nat_mode == "nstance-managed" ? "public" : "nstance"

  nat = var.nat_mode == "nstance-managed" ? {
    default = {
      group                = "nat"
      public_addresses     = try(module.network.public_addresses["public-us-central1-a"], [])
      instance_type_ladder = ["e2-micro"]
    }
  } : {}

  templates = {
    default = { kind = "dft", arch = "amd64" }
    nat     = { kind = "nat", arch = "amd64" }
  }

  groups = {
    "default" = merge({
      "workers" = {
        size        = 1
        subnet_pool = "workers" # References key from subnets map
      }
      }, var.nat_mode == "nstance-managed" ? {
      nat = {
        subnet_pool  = "public"
        template     = "nat"
        machine_type = "e2-micro"
      }
    } : {})
  }
}

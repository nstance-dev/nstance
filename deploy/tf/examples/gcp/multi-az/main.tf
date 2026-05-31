# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0
#
# Production Multi-Zone Deployment (GCP)
#
# This example demonstrates a production-ready multi-zone deployment with:
# - New VPC with subnets in multiple zones
# - Cloud NAT for outbound traffic (regional)
# - Private subnets for control-plane, ingress, and workers
# - Regional load balancer routing to ingress subnets

variable "project" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
}

variable "cluster_id" {
  description = "Cluster ID (lowercase alphanumeric with hyphens, max 32 chars)"
  type        = string
}

provider "google" {
  project = var.project
  region  = var.region
}

module "cluster" {
  source = "../../../gcp/cluster"

  cluster_id = var.cluster_id
}

module "account" {
  source  = "../../../gcp/account"
  cluster = module.cluster
}

module "network" {
  source = "../../../gcp/network"

  cluster       = module.cluster
  vpc_cidr_ipv4 = "10.0.0.0/16"

  subnets = {
    # Public subnets with Cloud NAT (regional NAT covers all zones)
    "public" = {
      "us-central1-a" = [{
        ipv4_cidr   = "10.0.0.0/24"
        ipv6_netnum = 0
        public      = true
        nat_gateway = true
      }]
      "us-central1-b" = [{
        ipv4_cidr   = "10.0.1.0/24"
        ipv6_netnum = 1
        public      = true
      }]
      "us-central1-c" = [{
        ipv4_cidr   = "10.0.2.0/24"
        ipv6_netnum = 2
        public      = true
      }]
    }

    # Server subnets for nstance-server instances
    "nstance" = {
      "us-central1-a" = [{
        ipv4_cidr   = "10.0.10.0/28"
        ipv6_netnum = 10
        nat_subnet  = "public"
      }]
      "us-central1-b" = [{
        ipv4_cidr   = "10.0.11.0/28"
        ipv6_netnum = 11
        nat_subnet  = "public"
      }]
      "us-central1-c" = [{
        ipv4_cidr   = "10.0.12.0/28"
        ipv6_netnum = 12
        nat_subnet  = "public"
      }]
    }

    # Control plane nodes
    "control-plane" = {
      "us-central1-a" = [{
        ipv4_cidr   = "10.0.20.0/24"
        ipv6_netnum = 20
        nat_subnet  = "public"
      }]
      "us-central1-b" = [{
        ipv4_cidr   = "10.0.21.0/24"
        ipv6_netnum = 21
        nat_subnet  = "public"
      }]
      "us-central1-c" = [{
        ipv4_cidr   = "10.0.22.0/24"
        ipv6_netnum = 22
        nat_subnet  = "public"
      }]
    }

    # Ingress nodes (for load balancer targets)
    "ingress" = {
      "us-central1-a" = [{
        ipv4_cidr   = "10.0.30.0/24"
        ipv6_netnum = 30
        nat_subnet  = "public"
      }]
      "us-central1-b" = [{
        ipv4_cidr   = "10.0.31.0/24"
        ipv6_netnum = 31
        nat_subnet  = "public"
      }]
      "us-central1-c" = [{
        ipv4_cidr   = "10.0.32.0/24"
        ipv6_netnum = 32
        nat_subnet  = "public"
      }]
    }

    # Worker nodes
    "workers" = {
      "us-central1-a" = [{
        ipv4_cidr   = "10.0.100.0/22"
        ipv6_netnum = 100
        nat_subnet  = "public"
      }]
      "us-central1-b" = [{
        ipv4_cidr   = "10.0.104.0/22"
        ipv6_netnum = 104
        nat_subnet  = "public"
      }]
      "us-central1-c" = [{
        ipv4_cidr   = "10.0.108.0/22"
        ipv6_netnum = 108
        nat_subnet  = "public"
      }]
    }
  }

  # Public load balancer on ports 80 and 443, placed in ingress subnets
  load_balancers = {
    www = { ports = [80, 443], subnets = "ingress", public = true }
  }
}

# Create shards for each zone
module "shard_a" {
  source = "../../../gcp/shard"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard = "us-central1-a"
  zone  = "us-central1-a"

  groups = {
    "default" = {
      "control-plane" = {
        size        = 3
        subnet_pool = "control-plane"
      }
      "ingress" = {
        size           = 2
        subnet_pool    = "ingress"
        load_balancers = ["www"] # Register instances with the www LB
      }
      "workers" = {
        size        = 10
        subnet_pool = "workers"
      }
    }
  }
}

module "shard_b" {
  source = "../../../gcp/shard"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard = "us-central1-b"
  zone  = "us-central1-b"

  groups = {
    "default" = {
      "control-plane" = {
        size        = 3
        subnet_pool = "control-plane"
      }
      "ingress" = {
        size           = 2
        subnet_pool    = "ingress"
        load_balancers = ["www"]
      }
      "workers" = {
        size        = 10
        subnet_pool = "workers"
      }
    }
  }
}

module "shard_c" {
  source = "../../../gcp/shard"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard = "us-central1-c"
  zone  = "us-central1-c"

  groups = {
    "default" = {
      "control-plane" = {
        size        = 3
        subnet_pool = "control-plane"
      }
      "ingress" = {
        size           = 2
        subnet_pool    = "ingress"
        load_balancers = ["www"]
      }
      "workers" = {
        size        = 10
        subnet_pool = "workers"
      }
    }
  }
}

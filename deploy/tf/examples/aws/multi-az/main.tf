# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0
#
# Production Multi-AZ Deployment (AWS)
#
# This example demonstrates a production-ready multi-AZ deployment with:
# - Existing VPC
# - Public subnets with NAT gateways (one per AZ for HA)
# - Existing database subnets (referenced only)
# - Private subnets for control-plane, ingress, and workers
# - NLB routing to ingress subnets

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
  source  = "../../../aws/account"
  cluster = module.cluster
}

module "network" {
  source = "../../../aws/network"

  cluster = module.cluster

  # Use existing VPC
  vpc_id = "vpc-prod123"

  subnets = {
    # Public subnets with NAT gateways (one per AZ for high availability)
    "public" = {
      "us-east-1a" = [{
        ipv4_cidr   = "10.0.0.0/24"
        public      = true
        nat_gateway = true
      }]
      "us-east-1b" = [{
        ipv4_cidr   = "10.0.1.0/24"
        public      = true
        nat_gateway = true
      }]
      "us-east-1c" = [{
        ipv4_cidr   = "10.0.2.0/24"
        public      = true
        nat_gateway = true
      }]
    }

    # Reference existing database subnets (no routing changes needed)
    "database" = {
      "us-east-1a" = [{ existing = "subnet-db-1a" }]
      "us-east-1b" = [{ existing = "subnet-db-1b" }]
      "us-east-1c" = [{ existing = "subnet-db-1c" }]
    }

    # Server subnets for nstance-server instances
    "nstance" = {
      "us-east-1a" = [{
        ipv4_cidr  = "10.0.10.0/28"
        nat_subnet = "public"
      }]
      "us-east-1b" = [{
        ipv4_cidr  = "10.0.11.0/28"
        nat_subnet = "public"
      }]
      "us-east-1c" = [{
        ipv4_cidr  = "10.0.12.0/28"
        nat_subnet = "public"
      }]
    }

    # Control plane nodes
    "control-plane" = {
      "us-east-1a" = [{
        ipv4_cidr  = "10.0.20.0/24"
        nat_subnet = "public"
      }]
      "us-east-1b" = [{
        ipv4_cidr  = "10.0.21.0/24"
        nat_subnet = "public"
      }]
      "us-east-1c" = [{
        ipv4_cidr  = "10.0.22.0/24"
        nat_subnet = "public"
      }]
    }

    # Ingress nodes (for load balancer targets)
    "ingress" = {
      "us-east-1a" = [{
        ipv4_cidr  = "10.0.30.0/24"
        nat_subnet = "public"
      }]
      "us-east-1b" = [{
        ipv4_cidr  = "10.0.31.0/24"
        nat_subnet = "public"
      }]
      "us-east-1c" = [{
        ipv4_cidr  = "10.0.32.0/24"
        nat_subnet = "public"
      }]
    }

    # Worker nodes
    "workers" = {
      "us-east-1a" = [{
        ipv4_cidr  = "10.0.100.0/22"
        nat_subnet = "public"
      }]
      "us-east-1b" = [{
        ipv4_cidr  = "10.0.104.0/22"
        nat_subnet = "public"
      }]
      "us-east-1c" = [{
        ipv4_cidr  = "10.0.108.0/22"
        nat_subnet = "public"
      }]
    }
  }

  # Public load balancer on ports 80 and 443, placed in ingress subnets
  load_balancers = {
    www = { ports = [80, 443], subnets = "ingress", public = true }
  }
}

# Create shards for each AZ
module "shard_1a" {
  source = "../../../aws/shard"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard = "us-east-1a"
  zone  = "us-east-1a"

  groups = {
    "default" = {
      "control-plane" = {
        size        = 3
        subnet_pool = "control-plane"
      }
      "ingress" = {
        size           = 2
        subnet_pool    = "ingress"
        load_balancers = ["www"] # Register instances with the www NLB
      }
      "workers" = {
        size        = 10
        subnet_pool = "workers"
      }
    }
  }
}

module "shard_1b" {
  source = "../../../aws/shard"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard = "us-east-1b"
  zone  = "us-east-1b"

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

module "shard_1c" {
  source = "../../../aws/shard"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard = "us-east-1c"
  zone  = "us-east-1c"

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

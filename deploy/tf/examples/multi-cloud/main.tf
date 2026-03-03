# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0
#
# Multi-Cloud Deployment (AWS + GCP)
#
# This example demonstrates a multi-cloud deployment with:
# - Cluster resources in AWS (bucket + secrets)
# - Shards in both AWS and GCP
# - Unified cluster coordination across clouds

variable "aws_profile" {
  description = "AWS CLI profile name"
  type        = string
}

variable "aws_region" {
  description = "AWS region"
  type        = string
}

variable "gcp_project" {
  description = "GCP project ID"
  type        = string
}

variable "gcp_region" {
  description = "GCP region"
  type        = string
}

variable "cluster_id" {
  description = "Cluster ID (lowercase alphanumeric with hyphens, max 32 chars)"
  type        = string
}

provider "aws" {
  profile = var.aws_profile
  region  = var.aws_region
}

provider "google" {
  project = var.gcp_project
  region  = var.gcp_region
}

# Cluster resources in AWS (bucket + secrets)
module "cluster" {
  source = "../../aws/cluster"

  aws_profile = var.aws_profile
  cluster_id  = var.cluster_id
}

# AWS account module
module "account_aws" {
  source = "../../aws/account"

  providers = {
    aws = aws
  }

  cluster = module.cluster
}

# AWS network
module "network_aws" {
  source = "../../aws/network"

  providers = {
    aws = aws
  }

  cluster       = module.cluster
  vpc_cidr_ipv4 = "172.18.0.0/16"

  subnets = {
    "public" = {
      "us-west-2a" = [{ ipv4_cidr = "172.18.0.0/28", ipv6_netnum = 0, public = true, nat_gateway = true }]
    }
    "nstance" = {
      "us-west-2a" = [{ ipv4_cidr = "172.18.1.0/28", ipv6_netnum = 1, nat_subnet = "public" }]
    }
    "workers" = {
      "us-west-2a" = [{ ipv4_cidr = "172.18.10.0/24", ipv6_netnum = 10, nat_subnet = "public" }]
    }
  }
}

# GCP account module
module "account_gcp" {
  source = "../../gcp/account"

  providers = {
    google = google
  }

  cluster = module.cluster
}

# GCP network
module "network_gcp" {
  source = "../../gcp/network"

  providers = {
    google = google
  }

  cluster       = module.cluster
  vpc_cidr_ipv4 = "172.19.0.0/16"

  subnets = {
    "public" = {
      "us-central1-a" = [{ ipv4_cidr = "172.19.0.0/28", ipv6_netnum = 0, public = true, nat_gateway = true }]
    }
    "nstance" = {
      "us-central1-a" = [{ ipv4_cidr = "172.19.1.0/28", ipv6_netnum = 1, nat_subnet = "public" }]
    }
    "workers" = {
      "us-central1-a" = [{ ipv4_cidr = "172.19.10.0/24", ipv6_netnum = 10, nat_subnet = "public" }]
    }
  }
}

module "shard_aws" {
  source = "../../aws/shard"

  providers = {
    aws = aws
  }

  cluster = module.cluster
  account = module.account_aws
  network = module.network_aws

  shard = "us-west-2a"
  zone  = "us-west-2a"

  groups = {
    "default" = {
      "workers" = {
        size        = 5
        subnet_pool = "workers"
      }
    }
  }
}

module "shard_gcp" {
  source = "../../gcp/shard"

  providers = {
    google = google
  }

  cluster = module.cluster
  account = module.account_gcp
  network = module.network_gcp

  shard = "us-central1-a"
  zone  = "us-central1-a"

  groups = {
    "default" = {
      "workers" = {
        size        = 5
        subnet_pool = "workers"
      }
    }
  }
}

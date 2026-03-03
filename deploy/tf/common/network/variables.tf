# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

################################################################################
# Module Dependencies
################################################################################

variable "cluster" {
  description = "Cluster configuration from cluster module output"
  type = object({
    name_prefix = optional(string, "nstance")
    shards      = optional(list(string), [])
  })
}

################################################################################
# VPC Configuration
################################################################################

variable "vpc_id" {
  description = "Existing VPC ID to use. If specified, skips VPC/IGW creation but still creates NAT gateways and route tables as defined in subnets."
  type        = string
  default     = ""
}

variable "vpc_cidr_ipv4" {
  description = "IPv4 CIDR block for the VPC. Required when creating a new VPC (vpc_id not set). Must be empty when using an existing VPC."
  type        = string
  default     = ""
}

variable "enable_ipv6" {
  description = "Enable IPv6 dual-stack support"
  type        = bool
  default     = true
}

variable "name_prefix" {
  description = "Prefix for resource names. Defaults to cluster.name_prefix if not set."
  type        = string
  default     = null
}

variable "enable_ssm" {
  description = "Enable VPC endpoints for SSM (AWS only)"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

variable "project_id" {
  description = "GCP project ID (optional, uses provider default if not set)"
  type        = string
  default     = ""
}

variable "region" {
  description = "GCP region (optional, uses provider default if not set)"
  type        = string
  default     = ""
}

variable "subnets" {
  description = <<-EOT
    Subnet definitions by role key and zone. Structure: role key -> zone -> list of subnet definitions.
    
    Each subnet definition is an object with:
    - ipv4_cidr   (string, optional) - Create a new subnet with this CIDR. Mutually exclusive with 'existing'.
    - ipv6_netnum (number, optional) - Subnet number (0-255) for auto-computed IPv6 /64 from VPC's /56.
    - ipv6_cidr   (string, optional) - Explicit IPv6 CIDR (alternative to ipv6_netnum).
    - existing    (string, optional) - Reference an existing subnet ID. Mutually exclusive with 'ipv4_cidr'.
    - public      (bool, default false) - Route via IGW, assign public IPs on launch.
    - nat_gateway (bool, default false) - Place a NAT gateway in this subnet. Requires public = true.
    - nat_subnet  (string, optional) - Route via NAT from this role key (same AZ), e.g., nat_subnet = "public".
    - shards      (list of strings, optional) - Restrict to specific shards.
    
    Routing behavior:
    - public = true: associates subnet with public route table (IGW route)
    - nat_subnet = "X": associates with private route table routing to NAT gateway in role "X" for same AZ
    - Neither: no route table association (isolated or user-managed)
    
    Example:
    subnets = {
      "public" = {
        "us-west-2a" = [{ ipv4_cidr = "172.18.0.0/28", public = true, nat_gateway = true }]
        "us-west-2b" = [{ ipv4_cidr = "172.18.0.16/28", public = true, nat_gateway = true }]
      }
      "workers" = {
        "us-west-2a" = [{ ipv4_cidr = "172.18.10.0/24", nat_subnet = "public" }]
        "us-west-2b" = [{ ipv4_cidr = "172.18.11.0/24", nat_subnet = "public" }]
      }
      "db" = {
        "us-west-2a" = [{ existing = "subnet-db-a", nat_subnet = "public" }]
        "us-west-2b" = [{ existing = "subnet-db-b" }]
      }
    }
  EOT
  type        = any
  default     = {}
}

variable "load_balancers" {
  description = <<-EOT
    Map of load balancer configurations. Each entry creates one regional NLB (AWS) or regional 
    load balancer (GCP) with listeners for all specified ports.
    
    The 'subnets' field references a role key from the subnets variable. The LB will be placed
    in all subnets matching that role (one per zone).
    
    The 'public' field controls whether the LB is internet-facing (true) or internal (false).
    On AWS, public load balancers require public subnets (with IGW routes).
    
    Example:
    load_balancers = {
      "www" = { ports = [80, 443], subnets = "ingress", public = true }
      "api" = { ports = [8080], subnets = "workers", public = false }
    }
  EOT
  type = map(object({
    ports   = list(number)
    subnets = string
    public  = bool
  }))
  default = {}
}

# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

################################################################################
# Module Dependencies
################################################################################

variable "cluster" {
  description = "Cluster configuration from cluster module output"
  type = object({
    id                      = string
    name_prefix             = optional(string, "nstance")
    shards                  = optional(list(string), [])
    secrets_provider        = string
    encryption_key_provider = optional(string, "")
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

variable "enable_interface_endpoints" {
  description = "Create billed AWS PrivateLink interface endpoints for configured AWS services"
  type        = bool
  default     = false
}

variable "enable_ssm" {
  description = "Include Session Manager endpoints when AWS interface endpoints are enabled"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

variable "project_id" {
  description = "Google Cloud project ID (optional, uses provider default if not set)"
  type        = string
  default     = ""
}

variable "region" {
  description = "Google Cloud region (optional, uses provider default if not set)"
  type        = string
  default     = ""
}

variable "nat_mode" {
  description = "NAT implementation: none, provider, or nstance"
  type        = string
  default     = "nstance"

  validation {
    condition     = contains(["none", "provider", "nstance"], var.nat_mode)
    error_message = "nat_mode must be one of: none, provider, nstance."
  }
}

variable "fixed_public_ipv4_count" {
  description = "Optional fixed public IPv4 addresses to pre-provision per NAT service subnet when using Nstance NAT instances"
  type        = number
  default     = 0

  validation {
    condition     = var.fixed_public_ipv4_count >= 0 && floor(var.fixed_public_ipv4_count) == var.fixed_public_ipv4_count
    error_message = "fixed_public_ipv4_count must be a non-negative integer."
  }
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
    - nat_gateway (bool, default false) - Use this public subnet for the selected NAT mode. Requires public = true.
    - nat_subnet  (string, optional) - Route via NAT from this role key (same AZ), e.g., nat_subnet = "public".
    - shards      (list of strings, optional) - Restrict to specific shards.
    
    Routing behavior:
    - public = true: associates subnet with public route table (IGW route)
    - nat_subnet = "X": routes translated egress through the selected NAT mode in role "X" for the same zone
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
    load balancer (Google Cloud) with all specified listeners. Each listener has
    an external port and an optional VM target port, which defaults to the external port.
    
    The 'subnets' field references the frontend subnet role. On Google Cloud,
    backend_subnets and proxy_subnets optionally name the production and
    nstance-server subnet roles; each defaults to subnets.
    
    The 'public' field controls whether the LB is internet-facing (true) or internal (false).
    On AWS, public load balancers require public subnets (with IGW routes).
    
    Example:
    load_balancers = {
      "www" = { listeners = [{ port = 80, target_port = 8080 }, { port = 443, target_port = 8443 }], subnets = "ingress", public = true }
      "api" = { listeners = [{ port = 8080 }], subnets = "workers", public = false }
    }
  EOT
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

  validation {
    condition = alltrue([
      for lb in values(var.load_balancers) :
      length(distinct([for listener in lb.listeners : listener.port])) == length(lb.listeners) &&
      alltrue([
        for listener in lb.listeners :
        listener.port >= 1 && listener.port <= 65535 && floor(listener.port) == listener.port &&
        (listener.target_port == null ? true : listener.target_port >= 1 && listener.target_port <= 65535 && floor(listener.target_port) == listener.target_port) &&
        (listener.proxy_port == null ? true : listener.proxy_port >= 1 && listener.proxy_port <= 65535 && floor(listener.proxy_port) == listener.proxy_port)
      ])
    ])
    error_message = "Load-balancer listener ports must be unique, and listener, target, and proxy ports must be whole numbers from 1 through 65535."
  }
}

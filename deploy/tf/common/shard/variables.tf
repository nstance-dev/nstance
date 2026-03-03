# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

################################################################################
# Module Dependencies
################################################################################

variable "cluster" {
  description = "Cluster configuration from cluster module"
  type = object({
    id                 = string
    name_prefix        = optional(string, "nstance")
    shards             = optional(list(string), [])
    bucket             = string
    bucket_arn         = optional(string, "")
    secrets_provider   = optional(string, "object-storage")
    encryption_key_source = optional(string, "")
    server_config = optional(object({
      request_timeout        = optional(string, "30s")
      create_rate_limit      = optional(string, "")
      health_check_interval  = optional(string, "60s")
      default_drain_timeout  = optional(string, "5m")
      image_refresh_interval = optional(string, "6h")
      shutdown_timeout       = optional(string, "10s")

      garbage_collection = optional(object({
        interval                 = optional(string, "2m")
        registration_timeout     = optional(string, "5m")
        deleted_record_retention = optional(string, "30m")
      }), {})
      leader_election = optional(object({
        frequent_interval   = optional(string, "5s")
        infrequent_interval = optional(string, "30s")
        leader_timeout      = optional(string, "15s")
      }), {})
      expiry = optional(object({
        eligible_age = optional(string, "")
        forced_age   = optional(string, "")
        ondemand_age = optional(string, "")
      }), {})
      error_exit_jitter = optional(object({
        min_delay = optional(string, "10s")
        max_delay = optional(string, "40s")
      }), {})
      bind = optional(object({
        health_addr       = optional(string, "0.0.0.0:8990")
        election_addr     = optional(string, "0.0.0.0:8991")
        registration_addr = optional(string, "0.0.0.0:8992")
        operator_addr     = optional(string, "0.0.0.0:8993")
        agent_addr        = optional(string, "0.0.0.0:8994")
      }), {})
      advertise = optional(object({
        health_addr       = optional(string, ":8990")
        election_addr     = optional(string, ":8991")
        registration_addr = optional(string, ":8992")
        operator_addr     = optional(string, ":8993")
        agent_addr        = optional(string, ":8994")
      }), {})
    }), {})
  })
}

variable "account" {
  description = "Account configuration from account module"
  type = object({
    server_iam_role_arn         = string
    agent_iam_role_arn          = string
    server_instance_profile_arn = optional(string, "")
    agent_instance_profile_arn  = optional(string, "")
  })
}

variable "network" {
  description = "Network configuration from network module or inline object"
  type = object({
    vpc_id                  = string
    vpc_cidr_ipv4           = string
    vpc_cidr_ipv6           = optional(string, null)
    enable_ipv6             = optional(bool, false)     # Known at plan time, use for count/for_each
    public_subnet_ids       = optional(map(string), {}) # zone -> subnet ID for NLB placement
    public_route_table_id   = optional(string, null)    # AWS only
    private_route_table_ids = optional(map(string), {}) # zone -> route table ID (AWS only)
    nat_gateway_ids         = optional(map(string), {}) # zone -> NAT gateway ID
    subnets                 = optional(any, {})         # role -> zone -> [{id, shards, public}]
    load_balancers = optional(map(object({
      dns_name          = optional(string, "")
      arn               = optional(string, "")
      target_group_arns = optional(list(string), [])
      ip_address        = optional(string, "")
      instance_groups   = optional(map(string), {})
    })), {})
  })
}

################################################################################
# Shard Configuration
################################################################################

variable "shard" {
  description = "Unique identifier for this shard. Must be in cluster.shards if that list is non-empty."
  type        = string
}

variable "zone" {
  description = "Availability zone (AWS) or zone (GCP) for this shard"
  type        = string
}

variable "server_subnet" {
  description = "Subnet role key from network.subnets for server instances"
  type        = string
  default     = "nstance"
}

variable "server_instance_type" {
  description = "Instance type for server (AWS)"
  type        = string
  default     = "t4g.nano"
}

variable "server_machine_type" {
  description = "Machine type for server (GCP)"
  type        = string
  default     = "e2-micro"
}

variable "server_count" {
  description = "Number of server instances (2+ for hot standby)"
  type        = number
  default     = 1
}

variable "server_arch" {
  description = "CPU architecture for server instances: arm64 or amd64. Defaults to arm64 on AWS (Graviton) and amd64 on GCP."
  type        = string
  default     = null # Provider-specific default applied in submodules

  validation {
    condition     = var.server_arch == null || contains(["arm64", "amd64"], var.server_arch)
    error_message = "server_arch must be 'arm64' or 'amd64'."
  }
}

variable "dynamic_subnet_pools" {
  description = "List of subnet pools allowed for dynamic groups (empty = all allowed)"
  type        = list(string)
  default     = []
}

variable "groups" {
  description = "Map of group configurations by tenant. Structure: tenant -> group_name -> config"
  type = map(map(object({
    size           = number
    subnet_pool    = string # References a subnet pool ID from server config
    instance_type  = optional(string, "t4g.nano")
    machine_type   = optional(string, "e2-micro")
    template       = optional(string, "default")
    load_balancers = optional(list(string), [])
    vars           = optional(map(string), {})
    drain_timeout  = optional(string, null)
  })))
}

################################################################################
# Binary Distribution
################################################################################

variable "nstance_version" {
  description = "Version of nstance to deploy (e.g., v0.1.0). If empty, uses 'latest' release."
  type        = string
  default     = ""
}

variable "nstance_server_binary_url" {
  description = "Fixed URL for nstance-server binary tarball. If set, bypasses GitHub release download."
  type        = string
  default     = ""
}

variable "nstance_agent_binary_url" {
  description = "Fixed URL for nstance-agent binary tarball. If set, bypasses GitHub release download."
  type        = string
  default     = ""
}

################################################################################
# Access Control
################################################################################

variable "ssh_key_name" {
  description = "Name of the SSH key pair for EC2 instances (AWS)"
  type        = string
  default     = ""
}

variable "ssh_keys" {
  description = "SSH public keys for instances (GCP)"
  type        = list(string)
  default     = []
}

variable "enable_ssm" {
  description = "Enable SSM Session Manager access (AWS)"
  type        = bool
  default     = true
}

variable "enable_os_login" {
  description = "Enable OS Login for IAM-based SSH (GCP)"
  type        = bool
  default     = true
}

variable "enable_iap" {
  description = "Enable IAP TCP tunneling for SSH (GCP)"
  type        = bool
  default     = true
}

################################################################################
# Agent Configuration
################################################################################

variable "agent_debug" {
  description = "Enable debug logging on agents"
  type        = bool
  default     = false
}

variable "agent_environment" {
  description = "Agent environment identifier"
  type        = string
  default     = "development"
}

variable "agent_spot_poll_interval" {
  description = "Agent spot termination polling interval"
  type        = string
  default     = "2s"
}

################################################################################
# Custom Templates Configuration
################################################################################

variable "templates" {
  description = "Instance templates. If empty, a default template is used. If specified, these templates are used as-is (no default merging)."
  type = map(object({
    kind          = string
    arch          = string
    instance_type = optional(string, "")
    machine_type  = optional(string, "")
    args          = optional(map(any), {})
    vars          = optional(map(string), {})
  }))
  default = {}
}

################################################################################
# Resource Tags
################################################################################

variable "name_prefix" {
  description = "Prefix for all resource names. Defaults to cluster.name_prefix if not set."
  type        = string
  default     = null
}

variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}

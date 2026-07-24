# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

################################################################################
# Module Dependencies
################################################################################

variable "cluster" {
  description = "Cluster configuration from cluster module"
  type = object({
    id                      = string
    name_prefix             = optional(string, "nstance")
    shards                  = optional(list(string), [])
    bucket                  = string
    bucket_arn              = optional(string, "")
    secrets_provider        = string
    encryption_key_provider = optional(string, "")
    encryption_key_source   = optional(string, "")
    project_id              = optional(string, "")
    server_config = optional(object({
      request_timeout        = optional(string)
      create_rate_limit      = optional(string)
      health_check_interval  = optional(string)
      default_drain_timeout  = optional(string)
      image_refresh_interval = optional(string)
      shutdown_timeout       = optional(string)
      cluster_leader_election = optional(object({
        frequent_interval   = optional(string)
        infrequent_interval = optional(string)
        leader_timeout      = optional(string)
      }), {})
      shard_leader_election = optional(object({
        frequent_interval   = optional(string)
        infrequent_interval = optional(string)
        leader_timeout      = optional(string)
      }), {})
      garbage_collection = optional(object({
        interval                 = optional(string)
        registration_timeout     = optional(string)
        deleted_record_retention = optional(string)
      }), {})
      expiry = optional(object({
        eligible_age = optional(string)
        forced_age   = optional(string)
        ondemand_age = optional(string)
      }), {})
      error_exit_jitter = optional(object({
        min_delay = optional(string)
        max_delay = optional(string)
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
      zone_id           = optional(string, "")
      security_group_id = optional(string, "")
      target_ports      = optional(list(number), [])
      target_group_arns = optional(map(string), {})
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
  description = "Availability zone (AWS) or zone (Google Cloud) for this shard"
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
  description = "Machine type for server (Google Cloud)"
  type        = string
  default     = "e2-micro"
}

variable "server_count" {
  description = "Number of server instances (2+ for hot standby)"
  type        = number
  default     = 1
}

variable "server_arch" {
  description = "CPU architecture for server instances: arm64 or amd64. Defaults to arm64 on AWS (Graviton) and amd64 on Google Cloud."
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
  description = "Map of group configurations by tenant. load_balancers maps each load balancer name to the listener ports this group serves; an empty list selects all listeners."
  type = map(map(object({
    size           = number
    subnet_pool    = string # References a subnet pool ID from server config
    instance_type  = optional(string, "t4g.nano")
    machine_type   = optional(string, "e2-micro")
    template       = optional(string, "default")
    load_balancers = optional(map(list(number)), {})
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
  description = "SSH public keys for instances (Google Cloud)"
  type        = list(string)
  default     = []
}

variable "enable_ssm" {
  description = "Enable SSM Session Manager access (AWS)"
  type        = bool
  default     = true
}

variable "enable_os_login" {
  description = "Enable OS Login for IAM-based SSH (Google Cloud)"
  type        = bool
  default     = true
}

variable "enable_iap" {
  description = "Enable IAP TCP tunneling for SSH (Google Cloud)"
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
  description = "Instance templates. If empty, a default template is used. Custom templates inherit default userdata and image args unless explicitly overridden."
  type = map(object({
    kind          = string
    arch          = string
    instance_type = optional(string, "")
    machine_type  = optional(string, "")
    args          = optional(any, {})
    vars          = optional(map(string), {})
    files         = optional(any, {})
    userdata      = optional(any, null)
  }))
  default = {}
}

variable "certificates" {
  description = "Certificate templates available to instance template files."
  type        = any
  default     = {}
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

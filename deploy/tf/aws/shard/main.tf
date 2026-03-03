# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# AWS Shard Module
# This module creates a single shard on AWS including:
# - Server subnet and group subnets
# - Security groups for server and agents
# - Server instances

data "aws_region" "current" {}

data "aws_caller_identity" "current" {}

locals {
  region      = data.aws_region.current.id
  account_id  = data.aws_caller_identity.current.account_id
  name_prefix = coalesce(var.name_prefix, var.cluster.name_prefix)
  shards      = var.cluster.shards

  # Extract port numbers from addr strings for security group rules and leader-service binds
  registration_port = tonumber(element(split(":", var.cluster.server_config.bind.registration_addr), length(split(":", var.cluster.server_config.bind.registration_addr)) - 1))
  operator_port     = tonumber(element(split(":", var.cluster.server_config.bind.operator_addr), length(split(":", var.cluster.server_config.bind.operator_addr)) - 1))
  agent_port        = tonumber(element(split(":", var.cluster.server_config.bind.agent_addr), length(split(":", var.cluster.server_config.bind.agent_addr)) - 1))
  health_port       = tonumber(element(split(":", var.cluster.server_config.bind.health_addr), length(split(":", var.cluster.server_config.bind.health_addr)) - 1))
  election_port     = tonumber(element(split(":", var.cluster.server_config.bind.election_addr), length(split(":", var.cluster.server_config.bind.election_addr)) - 1))

  # Apply provider-specific default for server_arch
  server_arch = coalesce(var.server_arch, "arm64")

  # Filter subnets for this shard based on shard and zone.
  # A subnet is included if:
  # 1. The subnet is in the shard's zone, AND
  # 2. Either: no shards filter (empty list), OR the shard is in the shards list
  filtered_subnets = {
    for role, zones in var.network.subnets : role => flatten([
      for zone, entries in zones : [
        for entry in entries : entry.id
        if zone == var.zone && (length(try(entry.shards, [])) == 0 || contains(try(entry.shards, []), var.shard))
      ]
    ])
    if length(flatten([
      for zone, entries in zones : [
        for entry in entries : entry.id
        if zone == var.zone && (length(try(entry.shards, [])) == 0 || contains(try(entry.shards, []), var.shard))
      ]
    ])) > 0
  }

  # Count total subnets found for validation
  total_filtered_subnets = sum([for role, ids in local.filtered_subnets : length(ids)])

  # Resolve server subnet ID from the server_subnet role
  server_subnet_id = try(local.filtered_subnets[var.server_subnet][0], "")

  # Common tags applied to all resources
  common_tags = merge(
    {
      "nstance:tf"         = "true"
      "nstance:cluster-id" = var.cluster.id
      "nstance:shard"      = var.shard
    },
    var.tags
  )

  # Version configuration
  nstance_version = var.nstance_version != "" ? var.nstance_version : "latest"
  github_repo     = "nstance-dev/nstance"

  # Build expiry config only if at least one expiry setting is configured
  expiry_config = (
    var.cluster.server_config.expiry.eligible_age != "" ||
    var.cluster.server_config.expiry.forced_age != "" ||
    var.cluster.server_config.expiry.ondemand_age != ""
    ) ? {
    expiry = merge(
      var.cluster.server_config.expiry.eligible_age != "" ? { eligible_age = var.cluster.server_config.expiry.eligible_age } : {},
      var.cluster.server_config.expiry.forced_age != "" ? { forced_age = var.cluster.server_config.expiry.forced_age } : {},
      var.cluster.server_config.expiry.ondemand_age != "" ? { ondemand_age = var.cluster.server_config.expiry.ondemand_age } : {}
    )
  } : {}

  # Default template used when no templates are specified
  default_template = {
    default = {
      kind     = "dft"
      arch     = "arm64"
      userdata = { content = local.agent_userdata_template }
      args = {
        ImageId = "{{ .Image.debian_13_arm64 }}"
      }
    }
  }

  # Use default template if none specified, otherwise use provided templates as-is
  templates = length(var.templates) == 0 ? local.default_template : {
    for name, tmpl in var.templates : name => merge(
      {
        kind = tmpl.kind
        arch = tmpl.arch
      },
      tmpl.instance_type != "" ? { instance_type = tmpl.instance_type } : {},
      length(tmpl.args) > 0 ? { args = tmpl.args } : {},
      length(tmpl.vars) > 0 ? { vars = tmpl.vars } : {}
    )
  }

  # Server userdata - rendered from template with Terraform variables
  server_userdata = templatefile("${path.module}/templates/server-userdata.sh.tpl", {
    nstance_version = local.nstance_version
    github_repo     = local.github_repo
    binary_url      = var.nstance_server_binary_url
    provider        = "aws"
    storage         = "s3"
    aws_region      = local.region
    gcp_project     = ""
    bucket          = var.cluster.bucket
    shard           = var.shard
    enable_ssm      = var.enable_ssm
  })

  # Agent userdata template - read as-is, uses Go text/template syntax
  # This gets stored in S3 config and interpolated by nstance-server at instance creation
  agent_userdata_template = templatefile("${path.module}/templates/agent-userdata.sh.tpl", {
    nstance_version        = local.nstance_version
    github_repo            = local.github_repo
    binary_url             = var.nstance_agent_binary_url
    provider               = "aws"
    enable_ssm             = var.enable_ssm
    agent_debug            = var.agent_debug
    agent_environment      = var.agent_environment
    agent_identity_mode    = "0600"
    agent_keys_mode        = "0640"
    agent_recv_mode        = "0640"
    agent_metrics_interval = var.cluster.server_config.health_check_interval
    agent_spot_poll        = var.agent_spot_poll_interval
  })
}

# Validate that at least one subnet was found (catches shard/zone typos)
resource "terraform_data" "validate_subnets" {
  lifecycle {
    precondition {
      condition     = local.total_filtered_subnets > 0
      error_message = "No subnets found for shard '${var.shard}' in zone '${var.zone}'. Check that the zone exists in the network module's subnets configuration and that shard matches any shard filters."
    }
  }
}

# Validate shard ID is in cluster.shards (when shards list is non-empty)
resource "terraform_data" "validate_shard" {
  count = length(local.shards) > 0 ? 1 : 0

  lifecycle {
    precondition {
      condition     = contains(local.shards, var.shard)
      error_message = "Shard '${var.shard}' is not in cluster.shards list: ${jsonencode(local.shards)}"
    }
  }
}

# Validate that the server_subnet role exists and has at least one subnet
resource "terraform_data" "validate_server_subnet" {
  lifecycle {
    precondition {
      condition     = contains(keys(local.filtered_subnets), var.server_subnet)
      error_message = "Server subnet role '${var.server_subnet}' not found. Available roles: ${join(", ", keys(local.filtered_subnets))}. Define it in network.subnets."
    }
    precondition {
      condition     = length(try(local.filtered_subnets[var.server_subnet], [])) > 0
      error_message = "No subnets found for server role '${var.server_subnet}' in zone '${var.zone}'."
    }
  }
}

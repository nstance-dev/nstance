# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

# ============================================================================
# Agent Userdata Template
# ============================================================================

locals {
  nstance_version = var.nstance_version != "" ? var.nstance_version : "latest"
  github_repo     = "nstance-dev/nstance"

  # Agent userdata template - read as-is, uses Go text/template syntax
  # This gets stored in GCS config and interpolated by nstance-server at instance creation
  agent_userdata_template = templatefile("${path.module}/templates/agent-userdata.sh.tpl", {
    nstance_version       = local.nstance_version
    github_repo           = local.github_repo
    binary_url            = var.nstance_agent_binary_url
    provider              = "google"
    enable_ssm            = false
    agent_debug           = var.agent_debug
    agent_environment     = var.agent_environment
    agent_identity_mode   = "0600"
    agent_keys_mode       = "0640"
    agent_recv_mode       = "0640"
    agent_report_interval = var.cluster.server_config.health_check_interval != null ? var.cluster.server_config.health_check_interval : ""
    agent_spot_poll       = var.agent_spot_poll_interval
  })

  cluster_leader_election_config = merge(
    var.cluster.server_config.cluster_leader_election.frequent_interval != null ? { frequent_interval = var.cluster.server_config.cluster_leader_election.frequent_interval } : {},
    var.cluster.server_config.cluster_leader_election.infrequent_interval != null ? { infrequent_interval = var.cluster.server_config.cluster_leader_election.infrequent_interval } : {},
    var.cluster.server_config.cluster_leader_election.leader_timeout != null ? { leader_timeout = var.cluster.server_config.cluster_leader_election.leader_timeout } : {}
  )

  shard_leader_election_config = merge(
    var.cluster.server_config.shard_leader_election.frequent_interval != null ? { frequent_interval = var.cluster.server_config.shard_leader_election.frequent_interval } : {},
    var.cluster.server_config.shard_leader_election.infrequent_interval != null ? { infrequent_interval = var.cluster.server_config.shard_leader_election.infrequent_interval } : {},
    var.cluster.server_config.shard_leader_election.leader_timeout != null ? { leader_timeout = var.cluster.server_config.shard_leader_election.leader_timeout } : {}
  )

  garbage_collection_config = merge(
    var.cluster.server_config.garbage_collection.interval != null ? { interval = var.cluster.server_config.garbage_collection.interval } : {},
    var.cluster.server_config.garbage_collection.registration_timeout != null ? { registration_timeout = var.cluster.server_config.garbage_collection.registration_timeout } : {},
    var.cluster.server_config.garbage_collection.deleted_record_retention != null ? { deleted_record_retention = var.cluster.server_config.garbage_collection.deleted_record_retention } : {}
  )

  expiry_config = merge(
    var.cluster.server_config.expiry.eligible_age != null && var.cluster.server_config.expiry.eligible_age != "" ? { eligible_age = var.cluster.server_config.expiry.eligible_age } : {},
    var.cluster.server_config.expiry.forced_age != null && var.cluster.server_config.expiry.forced_age != "" ? { forced_age = var.cluster.server_config.expiry.forced_age } : {},
    var.cluster.server_config.expiry.ondemand_age != null && var.cluster.server_config.expiry.ondemand_age != "" ? { ondemand_age = var.cluster.server_config.expiry.ondemand_age } : {}
  )

  error_exit_jitter_config = merge(
    var.cluster.server_config.error_exit_jitter.min_delay != null ? { min_delay = var.cluster.server_config.error_exit_jitter.min_delay } : {},
    var.cluster.server_config.error_exit_jitter.max_delay != null ? { max_delay = var.cluster.server_config.error_exit_jitter.max_delay } : {}
  )

  shard_optional_config = merge(
    var.cluster.server_config.request_timeout != null ? { request_timeout = var.cluster.server_config.request_timeout } : {},
    var.cluster.server_config.create_rate_limit != null && var.cluster.server_config.create_rate_limit != "" ? { create_rate_limit = var.cluster.server_config.create_rate_limit } : {},
    var.cluster.server_config.health_check_interval != null ? { health_check_interval = var.cluster.server_config.health_check_interval } : {},
    var.cluster.server_config.default_drain_timeout != null ? { default_drain_timeout = var.cluster.server_config.default_drain_timeout } : {},
    var.cluster.server_config.image_refresh_interval != null ? { image_refresh_interval = var.cluster.server_config.image_refresh_interval } : {},
    var.cluster.server_config.shutdown_timeout != null ? { shutdown_timeout = var.cluster.server_config.shutdown_timeout } : {},
    length(local.garbage_collection_config) > 0 ? { garbage_collection = local.garbage_collection_config } : {},
    length(local.expiry_config) > 0 ? { expiry = local.expiry_config } : {},
    length(local.shard_leader_election_config) > 0 ? { leader_election = local.shard_leader_election_config } : {},
    length(local.error_exit_jitter_config) > 0 ? { error_exit_jitter = local.error_exit_jitter_config } : {}
  )

  # Use default template if none specified. Custom templates inherit the default
  # userdata and image args unless explicitly overridden.
  template_names = length(var.templates) == 0 ? toset(["default"]) : toset(keys(var.templates))
  templates = {
    for name in local.template_names : name => merge(
      {
        kind     = try(var.templates[name].kind, "dft")
        arch     = try(var.templates[name].arch, "amd64")
        userdata = { content = local.agent_userdata_template }
        args = {
          SourceImage = "projects/debian-cloud/global/images/family/debian-13"
        }
      },
      try(var.templates[name].machine_type, "") != "" ? { instance_type = var.templates[name].machine_type } : {},
      length(try(var.templates[name].args, {})) > 0 ? { args = var.templates[name].args } : {},
      length(try(var.templates[name].vars, {})) > 0 ? { vars = var.templates[name].vars } : {},
      length(try(var.templates[name].files, {})) > 0 ? { files = var.templates[name].files } : {},
      try(var.templates[name].userdata, null) != null ? { userdata = var.templates[name].userdata } : {}
    )
  }
}

# ============================================================================
# Shard Config Upload
# ============================================================================

resource "google_storage_bucket_object" "shard_config" {
  bucket       = var.cluster.bucket
  name         = "shard/${var.shard}/config.jsonc"
  content_type = "application/json"

  content = jsonencode(merge(
    {
      cluster = merge(
        {
          id = var.cluster.id
          secrets = merge(
            {
              provider = var.cluster.secrets_provider == "object-storage" ? "object-storage" : var.cluster.secrets_provider
              prefix   = var.cluster.secrets_provider == "object-storage" ? "secret/" : "nstance-${var.cluster.id}-"
            },
            var.cluster.secrets_provider == "object-storage" ? {
              encryption_key = merge(
                {
                  provider = var.cluster.encryption_key_provider
                  source   = var.cluster.encryption_key_source
                },
                var.cluster.encryption_key_provider == "google-secret-manager" ? {
                  project_id = var.cluster.project_id
                } : {},
              )
            } : {},
            var.cluster.secrets_provider == "google-secret-manager" ? {
              project_id = var.cluster.project_id
            } : {},
          )
        },
        length(local.cluster_leader_election_config) > 0 ? { leader_election = local.cluster_leader_election_config } : {}
      )
      shard = merge(
        {
          id = var.shard
          infra = {
            provider = "google"
            region   = local.region
            zone     = var.zone
            options = {
              project_id = local.project_id
            }
          }
          leader_network = {
            ip = google_compute_address.server_leader.address
          }
          bind = {
            health_addr       = var.cluster.server_config.bind.health_addr
            election_addr     = var.cluster.server_config.bind.election_addr
            registration_addr = "${google_compute_address.server_leader.address}:${local.registration_port}"
            operator_addr     = "${google_compute_address.server_leader.address}:${local.operator_port}"
            agent_addr        = "${google_compute_address.server_leader.address}:${local.agent_port}"
          }
          advertise = {
            health_addr       = ":${local.health_port}"
            election_addr     = ":${local.election_port}"
            registration_addr = "${google_compute_address.server_leader.address}:${local.registration_port}"
            operator_addr     = "${google_compute_address.server_leader.address}:${local.operator_port}"
            agent_addr        = "${google_compute_address.server_leader.address}:${local.agent_port}"
          }
          subnet_pools         = local.filtered_subnets
          dynamic_subnet_pools = var.dynamic_subnet_pools
        },
        local.shard_optional_config
      )
      certificates = var.certificates
      templates    = local.templates
      load_balancers = {
        for lb_key, lb in var.network.load_balancers : lb_key => {
          provider            = "google"
          instance_group_name = try(lb.instance_groups[var.zone], "")
        }
        if try(lb.instance_groups[var.zone], "") != ""
      }
      groups = {
        # Groups are nested by tenant: { tenant -> { group_name -> GroupConfig } }
        for tenant, tenant_groups in var.groups : tenant => {
          for group_name, group in tenant_groups : group_name => merge(
            {
              template       = group.template
              size           = group.size
              instance_type  = group.machine_type
              subnet_pool    = group.subnet_pool
              load_balancers = keys(group.load_balancers)
              args = {
                ServiceAccount = var.account.agent_iam_role_arn
                NetworkTags    = ["nstance-agent-${var.shard}"]
              }
            },
            length(group.vars) > 0 ? { vars = group.vars } : {},
            group.drain_timeout != null ? { drain_timeout = group.drain_timeout } : {}
          )
        }
      }
    }
  ))

  depends_on = [
    google_compute_address.server_leader
  ]
}

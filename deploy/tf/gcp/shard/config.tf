# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# ============================================================================
# Agent Userdata Template
# ============================================================================

locals {
  nstance_version = var.nstance_version != "" ? var.nstance_version : "latest"
  github_repo     = "nstance-dev/nstance"

  agent_userdata_template = templatefile("${path.module}/templates/agent-userdata.sh.tpl", {
    nstance_version        = local.nstance_version
    github_repo            = local.github_repo
    binary_url             = var.nstance_agent_binary_url
    provider               = "gcp"
    enable_ssm             = false
    agent_debug            = var.agent_debug
    agent_environment      = var.agent_environment
    agent_identity_mode    = "0600"
    agent_keys_mode        = "0640"
    agent_recv_mode        = "0640"
    agent_metrics_interval = var.cluster.server_config.health_check_interval
    agent_spot_poll        = var.agent_spot_poll_interval
  })

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
      arch     = "amd64"
      userdata = { content = local.agent_userdata_template }
      args = {
        SourceImage = "projects/debian-cloud/global/images/family/debian-13"
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
      tmpl.machine_type != "" ? { instance_type = tmpl.machine_type } : {},
      length(tmpl.args) > 0 ? { args = tmpl.args } : {},
      length(tmpl.vars) > 0 ? { vars = tmpl.vars } : {}
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
      cluster = {
        id = var.cluster.id
        secrets = merge(
          {
            provider = var.cluster.secrets_provider == "object-storage" ? "object-storage" : var.cluster.secrets_provider
            prefix   = var.cluster.secrets_provider == "object-storage" ? "secret/" : "nstance-${var.cluster.id}-"
          },
          var.cluster.secrets_provider == "object-storage" ? {
            encryption_key = {
              provider = "gcp-secret-manager"
              source   = var.cluster.encryption_key_source
              options = {
                project_id = local.project_id
              }
            }
          } : {},
          var.cluster.secrets_provider != "object-storage" ? {
            options = {
              project_id = local.project_id
            }
          } : {},
        )
        leader_election = {
          enabled = true
        }
      }
      shard = merge(
        {
          id = var.shard
          infra = {
            provider = "gcp"
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
          request_timeout        = var.cluster.server_config.request_timeout
          default_drain_timeout  = var.cluster.server_config.default_drain_timeout
          health_check_interval  = var.cluster.server_config.health_check_interval
          image_refresh_interval = var.cluster.server_config.image_refresh_interval
          garbage_collection = {
            interval                 = var.cluster.server_config.garbage_collection.interval
            registration_timeout     = var.cluster.server_config.garbage_collection.registration_timeout
            deleted_record_retention = var.cluster.server_config.garbage_collection.deleted_record_retention
          }
          leader_election = {
            frequent_interval   = var.cluster.server_config.leader_election.frequent_interval
            infrequent_interval = var.cluster.server_config.leader_election.infrequent_interval
            leader_timeout      = var.cluster.server_config.leader_election.leader_timeout
          }
          error_exit_jitter = {
            min_delay = var.cluster.server_config.error_exit_jitter.min_delay
            max_delay = var.cluster.server_config.error_exit_jitter.max_delay
          }
          subnet_pools         = local.filtered_subnets
          dynamic_subnet_pools = var.dynamic_subnet_pools
        },
        var.cluster.server_config.create_rate_limit != "" ? { create_rate_limit = var.cluster.server_config.create_rate_limit } : {}
      )
      templates = local.templates
      load_balancers = {
        for lb_key, lb in var.network.load_balancers : lb_key => {
          provider            = "gcp"
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
              load_balancers = group.load_balancers
              args = {
                ServiceAccount = var.account.agent_iam_role_arn
                NetworkTags    = ["nstance-agent-${var.shard}"]
                Labels = {
                  "nstance-managed" = "true"
                  "nstance-group"   = group_name
                }
              }
            },
            length(group.vars) > 0 ? { vars = group.vars } : {},
            group.drain_timeout != null ? { drain_timeout = group.drain_timeout } : {}
          )
        }
      }
    },
    local.expiry_config
  ))

  depends_on = [
    google_compute_address.server_leader
  ]
}

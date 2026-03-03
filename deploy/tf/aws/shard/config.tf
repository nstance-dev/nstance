# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# Upload shard config file
resource "aws_s3_object" "shard_config" {
  bucket       = var.cluster.bucket
  key          = "shard/${var.shard}/config.jsonc"
  content_type = "application/json"

  content = jsonencode(merge(
    {
      cluster = {
        id = var.cluster.id
        secrets = var.cluster.secrets_provider == "object-storage" ? {
          provider = "object-storage"
          prefix   = "secret/"
          encryption_key = {
            provider = "aws-secrets-manager"
            source   = var.cluster.encryption_key_source
          }
          } : {
          provider       = var.cluster.secrets_provider
          prefix         = "nstance/${var.cluster.id}/"
          encryption_key = null
        }
        leader_election = {
          enabled = true
        }
      }
      shard = merge(
        {
          id = var.shard
          infra = {
            provider = "aws"
            region   = local.region
            zone     = var.zone
          }
          leader_network = {
            ip           = aws_network_interface.server_leader.private_ip
            interface_id = aws_network_interface.server_leader.id
          }
          bind = {
            health_addr       = var.cluster.server_config.bind.health_addr
            election_addr     = var.cluster.server_config.bind.election_addr
            registration_addr = "${aws_network_interface.server_leader.private_ip}:${local.registration_port}"
            operator_addr     = "${aws_network_interface.server_leader.private_ip}:${local.operator_port}"
            agent_addr        = "${aws_network_interface.server_leader.private_ip}:${local.agent_port}"
          }
          advertise = {
            health_addr       = ":${local.health_port}"
            election_addr     = ":${local.election_port}"
            registration_addr = "${aws_network_interface.server_leader.private_ip}:${local.registration_port}"
            operator_addr     = "${aws_network_interface.server_leader.private_ip}:${local.operator_port}"
            agent_addr        = "${aws_network_interface.server_leader.private_ip}:${local.agent_port}"
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
          provider          = "aws"
          target_group_arns = lb.target_group_arns
        }
      }
      groups = {
        # Groups are nested by tenant: { tenant -> { group_name -> GroupConfig } }
        for tenant, tenant_groups in var.groups : tenant => {
          for group_name, group in tenant_groups : group_name => merge(
            {
              template       = group.template
              size           = group.size
              instance_type  = group.instance_type
              subnet_pool    = group.subnet_pool
              load_balancers = group.load_balancers
              args = {
                IamInstanceProfile = {
                  Arn = var.account.agent_instance_profile_arn
                }
                SecurityGroupIds = [aws_security_group.agent.id]
                TagSpecifications = [
                  {
                    ResourceType = "instance"
                    Tags = [
                      { Key = "Name", Value = "{{ .Instance.ID }}" }
                    ]
                  }
                ]
              }
            },
            # Add group vars if configured
            length(group.vars) > 0 ? { vars = group.vars } : {},
            # Add drain timeout if configured
            group.drain_timeout != null ? { drain_timeout = group.drain_timeout } : {}
          )
        }
      }
      images = {
        debian_13_arm64 = {
          provider = "aws"
          filters = [
            { name = "name", values = ["debian-13-arm64-*"] },
            { name = "virtualization-type", values = ["hvm"] },
            { name = "architecture", values = ["arm64"] }
          ]
          owners = ["136693071363"]
          sort   = "creation-date"
          order  = "desc"
        }
        debian_13_amd64 = {
          provider = "aws"
          filters = [
            { name = "name", values = ["debian-13-amd64-*"] },
            { name = "virtualization-type", values = ["hvm"] },
            { name = "architecture", values = ["x86_64"] }
          ]
          owners = ["136693071363"]
          sort   = "creation-date"
          order  = "desc"
        }
      }
    },
    # Add expiry config if any expiry setting is configured
    local.expiry_config
  ))

  depends_on = [
    aws_network_interface.server_leader,
    aws_security_group.agent
  ]
}

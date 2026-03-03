# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

output "shard" {
  description = "The shard ID"
  value       = var.shard
}

output "zone" {
  description = "The zone for this shard"
  value       = var.zone
}

output "server_ips" {
  description = "List of server private IPs"
  value       = [aws_network_interface.server_leader.private_ip]
}

output "server_ids" {
  description = "List of server instance IDs"
  value       = [for i in range(var.server_count) : aws_autoscaling_group.server.name]
}

output "config_key" {
  description = "S3 key for shard config"
  value       = aws_s3_object.shard_config.key
}

output "load_balancers" {
  description = "Map of load balancer DNS names"
  value = {
    for lb_key, lb in var.network.load_balancers : lb_key => {
      dns_name = lb.dns_name
      arn      = lb.arn
    }
  }
}

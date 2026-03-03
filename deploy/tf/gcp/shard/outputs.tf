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
  value       = [google_compute_address.server_leader.address]
}

output "server_ids" {
  description = "List of server instance IDs"
  value       = [google_compute_instance_group_manager.server.instance_group]
}

output "config_key" {
  description = "GCS key for shard config"
  value       = google_storage_bucket_object.shard_config.name
}

output "load_balancers" {
  description = "Map of load balancer IPs"
  value = {
    for lb_key, lb in var.network.load_balancers : lb_key => {
      ip_address = lb.ip_address
    }
  }
}

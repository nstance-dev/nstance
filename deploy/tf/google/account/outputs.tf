# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

output "server_iam_role_arn" {
  description = "Server service account email"
  value       = google_service_account.server.email
}

output "agent_iam_role_arn" {
  description = "Agent service account email"
  value       = google_service_account.agent.email
}

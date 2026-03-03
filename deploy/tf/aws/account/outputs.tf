# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

output "server_iam_role_arn" {
  description = "Server IAM role ARN"
  value       = aws_iam_role.server.arn
}

output "agent_iam_role_arn" {
  description = "Agent IAM role ARN"
  value       = aws_iam_role.agent.arn
}

output "server_instance_profile_arn" {
  description = "Server instance profile ARN"
  value       = aws_iam_instance_profile.server.arn
}

output "agent_instance_profile_arn" {
  description = "Agent instance profile ARN"
  value       = aws_iam_instance_profile.agent.arn
}

# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

variable "cluster" {
  description = "Cluster configuration from cluster module output"
  type = object({
    name_prefix        = optional(string, "nstance")
    bucket_arn         = optional(string, "")
    bucket_name        = optional(string, "")
    encryption_key_source = optional(string, "")
    project_id         = optional(string, "")
  })
}

variable "name_prefix" {
  description = "Prefix for all resource names. Defaults to cluster.name_prefix if not set."
  type        = string
  default     = null
}

variable "enable_ssm" {
  description = "Enable SSM Session Manager access to instances (AWS only)"
  type        = bool
  default     = true
}

variable "enable_os_login" {
  description = "Enable OS Login for IAM-based SSH identity (GCP only)"
  type        = bool
  default     = true
}

variable "enable_iap" {
  description = "Enable IAP TCP tunneling for SSH access (GCP only)"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}

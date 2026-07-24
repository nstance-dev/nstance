# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

variable "cluster" {
  description = "Cluster configuration from cluster module output"
  type = object({
    id                      = string
    name_prefix             = optional(string, "nstance")
    bucket_arn              = optional(string, "")
    bucket_name             = optional(string, "")
    secrets_provider        = string
    secrets_prefix          = optional(string, "")
    encryption_key_provider = optional(string, "")
    encryption_key_source   = optional(string, "")
    project_id              = optional(string, "")
  })
}

variable "enable_ssm" {
  description = "Enable SSM Session Manager access to instances (AWS only)"
  type        = bool
  default     = true
}

variable "enable_os_login" {
  description = "Enable OS Login for IAM-based SSH identity (Google Cloud only)"
  type        = bool
  default     = true
}

variable "enable_iap" {
  description = "Enable IAP TCP tunneling for SSH access (Google Cloud only)"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}

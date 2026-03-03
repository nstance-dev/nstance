# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

variable "aws_profile" {
  description = "AWS CLI profile name (passed to local-exec provisioners to match the provider's credentials)"
  type        = string
  default     = null
}

variable "name_prefix" {
  description = "Prefix for resource names"
  type        = string
  default     = "nstance"

  validation {
    condition     = var.name_prefix != null && var.name_prefix != ""
    error_message = "name_prefix must not be empty or null"
  }
}

variable "cluster_id" {
  description = "Cluster ID (lowercase alphanumeric with hyphens, max 32 chars)"
  type        = string
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

variable "shards" {
  description = <<-EOT
    Optional list of valid shard IDs. When specified, other modules validate that shard 
    references are in this list, catching typos at plan time.
    
    If not specified (empty list), no validation is performed.
    
    Example:
    shards = ["us-east-1a-1", "us-east-1a-2", "us-east-1b-1", "us-east-1b-2"]
  EOT
  type        = list(string)
  default     = []
}

variable "bucket" {
  description = "Existing S3/GCS bucket (if empty, a new bucket is created)"
  type        = string
  default     = ""
}

variable "versioning" {
  description = "Enable object versioning on the bucket (increases storage costs)"
  type        = bool
  default     = false
}

variable "secrets_provider" {
  description = "Secrets storage provider: 'object-storage' (encrypted in bucket), 'aws-secrets-manager', or 'gcp-secret-manager'"
  type        = string
  default     = "object-storage"

  validation {
    condition     = contains(["object-storage", "aws-secrets-manager", "gcp-secret-manager"], var.secrets_provider)
    error_message = "secrets_provider must be one of: object-storage, aws-secrets-manager, gcp-secret-manager"
  }
}

variable "encryption_key" {
  description = "Existing encryption key secret (AWS: ARN, GCP: secret name). Only used when secrets_provider='object-storage'. If empty, a new secret is created."
  type        = string
  default     = ""
}

variable "server_config" {
  description = "Server configuration (merged over defaults). Structure matches ServerConfig JSON fields."
  type = object({
    request_timeout        = optional(string) # request_timeout: Request timeout (e.g., "30s")
    create_rate_limit      = optional(string) # create_rate_limit: Duration between instance creates (e.g., "100ms")
    health_check_interval  = optional(string) # health_check_interval: Expected agent health report interval (e.g., "60s")
    default_drain_timeout  = optional(string) # default_drain_timeout: Drain timeout before force delete (e.g., "5m")
    image_refresh_interval = optional(string) # image_refresh_interval: Image resolution refresh interval (e.g., "6h")
    shutdown_timeout       = optional(string) # shutdown_timeout: How long to wait for graceful shutdown (e.g., "30s")


    garbage_collection = optional(object({
      interval                 = optional(string) # How often to run GC (e.g., "2m")
      registration_timeout     = optional(string) # Wait for registration before terminating (e.g., "5m")
      deleted_record_retention = optional(string) # Keep deleted records for (e.g., "30m")
    }))

    leader_election = optional(object({
      frequent_interval   = optional(string) # Polling during transitions (e.g., "5s")
      infrequent_interval = optional(string) # Polling during stable periods (e.g., "30s")
      leader_timeout      = optional(string) # Time before considering leader failed (e.g., "15s")
    }))

    expiry = optional(object({
      eligible_age = optional(string) # Age for opportunistic expiry (e.g., "168h")
      forced_age   = optional(string) # Age for forced expiry (e.g., "720h")
      ondemand_age = optional(string) # Max age for on-demand instances (e.g., "720h")
    }))

    error_exit_jitter = optional(object({
      min_delay = optional(string) # Min delay before exit on error (e.g., "10s")
      max_delay = optional(string) # Max delay before exit on error (e.g., "40s")
    }))

    bind = optional(object({
      health_addr       = optional(string) # Addr for HTTP health endpoint (e.g., "0.0.0.0:8990")
      election_addr     = optional(string) # Addr for HTTPS leader election endpoint (e.g., "0.0.0.0:8991")
      registration_addr = optional(string) # Addr for registration service (e.g., "0.0.0.0:8992")
      operator_addr     = optional(string) # Addr for operator service (e.g., "0.0.0.0:8993")
      agent_addr        = optional(string) # Addr for agent service (e.g., "0.0.0.0:8994")
    }))

    advertise = optional(object({
      health_addr       = optional(string) # Addr for HTTP health endpoint (e.g., ":8990")
      election_addr     = optional(string) # Addr for HTTPS leader election endpoint (e.g., ":8991")
      registration_addr = optional(string) # Addr for registration service (e.g., ":8992")
      operator_addr     = optional(string) # Addr for operator service (e.g., ":8993")
      agent_addr        = optional(string) # Addr for agent service (e.g., ":8994")
    }))
  })
  default = {}
}

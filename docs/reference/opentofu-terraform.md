---
title: "OpenTofu / Terraform"
weight: 5
description: "OpenTofu/Terraform modules for deploying Nstance infrastructure across AWS and Google Cloud."
---

# Nstance OpenTofu/Terraform Modules

Nstance provides OpenTofu/Terraform modules with a unified, cloud-agnostic interface for deploying Nstance in Amazon Web Services (AWS) and/or Google Cloud (Google Cloud). Each module has a consistent variable interface with cloud-specific implementations underneath:

- `cluster` generates a cluster ID, a baseline configuration, and provisions or links cluster-wide resources: a S3/GCS bucket, and encrypted secrets.

- `account` creates IAM roles/instance profiles per AWS account/Google Cloud project.

- `network` VPC/network setup per account/project & region.

- `shard` deploys one Nstance zone "shard" (nstance-server instance + group instances).

The delineation of these modules enables cluster deployments to scale from single account/zone to multi-cloud/account/zone with a unified configuration.

## Deployed Resources Per-Module

| Resource Type                      | cluster | account | network | shard |
|------------------------------------|:-------:|:-------:|:-------:|:-----:|
| Cluster ID / Root CA               |    ✓    |         |         |       |
| S3/GCS Bucket                      |    ✓    |         |         |       |
| Optional Object-Storage Enc. Key   |    ✓    |         |         |       |
| Server IAM Role                    |         |    ✓    |         |       |
| Agent IAM Role                     |         |    ✓    |         |       |
| Instance Profiles                  |         |    ✓    |         |       |
| VPC/VPC Network                    |         |         |    ✓    |       |
| Internet Gateway                   |         |         |    ✓    |       |
| NAT Gateway/Cloud NAT              |         |         |    ✓    |       |
| Route Tables                       |         |         |    ✓    |       |
| VPC Endpoints (S3, SSM, etc.)      |         |         |    ✓    |       |
| Group Subnets (for agents)         |         |         |    ✓    |       |
| Server Subnets                     |         |         |    ✓    |       |
| Load Balancers (NLB / Regional LB) |         |         |    ✓    |       |
| Security Groups/Firewall Rules     |         |         |         |   ✓   |
| Server Instances                   |         |         |         |   ✓   |
| Group Instances                    |         |         |         |   *   |
| Shard Config (S3 object)           |         |         |         |   ✓   |

*Group Instances are provisioned by nstance-server, not OpenTofu/Terraform.

## Cloud-Specific Modules

Each cloud provider has its own published module repository:

- **AWS**: `nstance-dev/nstance/aws//modules/{module}`
  - Source: `github.com/nstance-dev/terraform-aws-nstance//{module}`
- **Google Cloud**: `nstance-dev/nstance/google//modules/{module}`
  - Source: `github.com/nstance-dev/terraform-google-nstance//{module}`

Region and project are inferred from the provider configuration via data sources (`data.aws_region.current` or `data.google_client_config.current`), to minimise the number of required variables per module.

## Development Structure

Module source code lives in the main `github.com/nstance-dev/nstance` repository under `deploy/tf/`. Modules share a unified variable interface — common variable definitions live in `deploy/tf/common/` and are symlinked into each cloud module. During release, the cloud-specific modules are synced to their respective repositories with symlinks replaced by actual files.

```
deploy/tf/
├── common/             # Shared variable definitions (symlinked into cloud modules)
│   ├── cluster/
│   │   └── variables.tf
│   ├── account/
│   │   └── variables.tf
│   ├── network/
│   │   └── variables.tf
│   └── shard/
│       └── variables.tf
│
├── aws/                # AWS-specific implementations → synced to terraform-aws-nstance
│   ├── cluster/
│   │   ├── main.tf
│   │   ├── outputs.tf
│   │   ├── versions.tf
│   │   └── variables.tf -> ../../common/cluster/variables.tf
│   ├── account/
│   ├── network/
│   └── shard/
│
├── google/             # Google Cloud-specific implementations → synced to terraform-google-nstance
│   ├── cluster/
│   │   ├── main.tf
│   │   ├── outputs.tf
│   │   ├── versions.tf
│   │   └── variables.tf -> ../../common/cluster/variables.tf
│   ├── account/
│   ├── network/
│   └── shard/
│
└── examples/           # Example configurations (synced to respective repos)
    ├── aws/
    │   ├── single-shard/
    │   └── multi-az/
    ├── google/
    │   ├── single-shard/
    │   └── multi-az/
    └── multi-cloud/
```

## Prerequisites

- OpenTofu >= 1.6.0 or Terraform >= 1.5.0
- The AWS cluster module requires Terraform/OpenTofu >= 1.11 and AWS provider >= 6.8.
- Cloud provider CLI (`aws` or `gcloud`) configured with appropriate credentials.
- GitHub releases available for nstance-server and nstance-agent (or custom binary URLs).

**Google Cloud Note:** The cluster module automatically enables required Google Cloud APIs (Compute, Secret Manager, Storage, IAM, IAP) on first apply, if not already enabled. If all services need to be enabled it adds ~30-60 seconds to the initial deployment but eliminates manual `gcloud services enable` commands.

## Security

- All instances run in private subnets by default, with public/NAT egress options available.
- Automatic VPC endpoints eliminate need for internet access to cloud services.
- Instance metadata service configuration uses secure defaults (i.e. IMDSv2 on AWS).
- Instance volumes are encrypted by default.
- IAM roles are separated per use case, each with least-privilege permissions.
- On AWS, secrets default to Parameter Store; Secrets Manager and object storage are explicit alternatives.
- When AWS object storage uses a Parameter Store encryption key and no existing key is supplied, Terraform safely creates a 32-character key using an ephemeral value and the AWS provider's write-only parameter value. Terraform/OpenTofu >= 1.11 and AWS provider >= 6.8 keep that value out of state.
- S3 buckets are encrypted by default.
- S3/GCS buckets have deletion protection by default.

## IPv4+IPv6 Dual-Stack Support

Both AWS and Google Cloud support IPv4+IPv6 dual-stack networking. IPv6 is enabled by default, but can be disabled by setting `enable_ipv6 = false` for the network module.

### Provider Differences

|                | AWS                                          | Google Cloud                            |
|----------------|----------------------------------------------|--------------------------------|
| IPv6 type      | Amazon-provided public /56                   | Internal ULA /48 (private)     |
| Address scope  | Globally routable                            | VPC-internal only              |
| Assignment     | Auto-generated on VPC creation               | Auto-generated on VPC creation |
| Subnet CIDRs   | Specify `ipv6_netnum` (0-255) per subnet     | Specify `ipv6_netnum` (0-65535) per subnet |
| Private egress | Egress-only Internet Gateway                 | Cloud NAT (same as IPv4)       |

### Using IPv6

When `enable_ipv6 = true` (the default), each subnet needs an IPv6 CIDR. You can specify this in two ways:

- **`ipv6_netnum`** (recommended) - Subnet number that auto-computes a /64 from the VPC's cloud-assigned block (AWS: 0-255 from /56, Google Cloud: 0-65535 from /48)
- **`ipv6_cidr`** - Explicit IPv6 CIDR block

```hcl
module "network" {
  source  = "nstance-dev/nstance/aws//modules/network"
  version = "~> 1.0"

  vpc_cidr_ipv4 = "172.18.0.0/16"

  subnets = {
    "public" = {
      "us-west-2a" = [{
        ipv4_cidr   = "172.18.0.0/24"
        ipv6_netnum = 0  # Auto-computes /64 from VPC's /56
        public      = true
        nat_gateway = true
      }]
    }
    "nstance" = {
      "us-west-2a" = [{
        ipv4_cidr   = "172.18.1.0/28"
        ipv6_netnum = 1
        nat_subnet  = "public"
      }]
    }
  }
}
```

### Disabling IPv6

To disable IPv6 and use IPv4-only networking:

```hcl
module "network" {
  source  = "nstance-dev/nstance/aws//modules/network"
  version = "~> 1.0"

  vpc_cidr_ipv4 = "172.18.0.0/16"
  enable_ipv6   = false

  subnets = {
    "nstance" = {
      "us-west-2a" = [{
        ipv4_cidr  = "172.18.1.0/28"
        nat_subnet = "public"
      }]
    }
  }
}
```

## Quick Start

### Minimal Single-Shard Deployment (AWS)

```hcl
provider "aws" {
  region = "us-west-2"
}

module "cluster" {
  source  = "nstance-dev/nstance/aws//modules/cluster"
  version = "~> 1.0"
}

module "account" {
  source  = "nstance-dev/nstance/aws//modules/account"
  version = "~> 1.0"

  cluster = module.cluster
}

module "network" {
  source  = "nstance-dev/nstance/aws//modules/network"
  version = "~> 1.0"

  cluster       = module.cluster
  vpc_cidr_ipv4 = "172.18.0.0/16"

  # Define subnets by role and zone
  subnets = {
    # Public subnet with NAT gateway for outbound traffic
    "public" = {
      "us-west-2a" = [{
        ipv4_cidr   = "172.18.0.0/24"
        public      = true
        nat_gateway = true
      }]
    }
    # Nstance Server subnet routes through NAT
    "nstance" = {
      "us-west-2a" = [{
        ipv4_cidr  = "172.18.1.0/28"
        nat_subnet = "public"
      }]
    }
    # Worker subnet routes through NAT
    "workers" = {
      "us-west-2a" = [{
        ipv4_cidr  = "172.18.10.0/24"
        nat_subnet = "public"
      }]
    }
  }
}

module "shard" {
  source  = "nstance-dev/nstance/aws//modules/shard"
  version = "~> 1.0"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard   = "us-west-2a"
  zone    = "us-west-2a"
  # server_subnet defaults to "nstance" - uses first subnet from that role in zone

  groups = {
    "default" = {
      "workers" = {
        size    = 1
        subnet_pool = "workers" # References key from subnets map
      }
    }
  }
}
```

### Production Multi-AZ Deployment (AWS)

This example demonstrates a production-ready multi-AZ deployment with:
- Existing VPC
- Public subnets with NAT gateways (one per AZ for HA)
- Existing database subnets (referenced only)
- Private subnets for control-plane, ingress, and workers
- NLB routing to ingress subnets

```hcl
provider "aws" {
  region = "us-east-1"
}

module "cluster" {
  source  = "nstance-dev/nstance/aws//modules/cluster"
  version = "~> 1.0"
}

module "account" {
  source  = "nstance-dev/nstance/aws//modules/account"
  version = "~> 1.0"

  cluster = module.cluster
}

module "network" {
  source  = "nstance-dev/nstance/aws//modules/network"
  version = "~> 1.0"

  cluster = module.cluster

  # Use existing VPC
  vpc_id = "vpc-prod123"

  subnets = {
    # Public subnets with NAT gateways (one per AZ for high availability)
    "public" = {
      "us-east-1a" = [{ ipv4_cidr = "10.0.0.0/24", public = true, nat_gateway = true }]
      "us-east-1b" = [{ ipv4_cidr = "10.0.1.0/24", public = true, nat_gateway = true }]
      "us-east-1c" = [{ ipv4_cidr = "10.0.2.0/24", public = true, nat_gateway = true }]
    }

    # Reference existing database subnets (no routing changes needed)
    "database" = {
      "us-east-1a" = [{ existing = "subnet-db-1a" }]
      "us-east-1b" = [{ existing = "subnet-db-1b" }]
      "us-east-1c" = [{ existing = "subnet-db-1c" }]
    }

    # Server subnets for nstance-server instances
    "nstance" = {
      "us-east-1a" = [{ ipv4_cidr = "10.0.10.0/28", nat_subnet = "public" }]
      "us-east-1b" = [{ ipv4_cidr = "10.0.11.0/28", nat_subnet = "public" }]
      "us-east-1c" = [{ ipv4_cidr = "10.0.12.0/28", nat_subnet = "public" }]
    }

    # Control plane, ingress, and worker nodes
    "control-plane" = {
      "us-east-1a" = [{ ipv4_cidr = "10.0.20.0/24", nat_subnet = "public" }]
      "us-east-1b" = [{ ipv4_cidr = "10.0.21.0/24", nat_subnet = "public" }]
      "us-east-1c" = [{ ipv4_cidr = "10.0.22.0/24", nat_subnet = "public" }]
    }
    "ingress" = {
      "us-east-1a" = [{ ipv4_cidr = "10.0.30.0/24", nat_subnet = "public" }]
      "us-east-1b" = [{ ipv4_cidr = "10.0.31.0/24", nat_subnet = "public" }]
      "us-east-1c" = [{ ipv4_cidr = "10.0.32.0/24", nat_subnet = "public" }]
    }
    "workers" = {
      "us-east-1a" = [{ ipv4_cidr = "10.0.100.0/22", nat_subnet = "public" }]
      "us-east-1b" = [{ ipv4_cidr = "10.0.104.0/22", nat_subnet = "public" }]
      "us-east-1c" = [{ ipv4_cidr = "10.0.108.0/22", nat_subnet = "public" }]
    }
  }

  # Public load balancer on ports 80 and 443, placed in ingress subnets
  load_balancers = {
    www = { listeners = [{ port = 80 }, { port = 443 }], subnets = "ingress", public = true }
  }
}

# Create shards for each AZ
module "shard_1a" {
  source  = "nstance-dev/nstance/aws//modules/shard"
  version = "~> 1.0"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard = "us-east-1a"
  zone  = "us-east-1a"

  groups = {
    "default" = {
      "control-plane" = { size = 3, subnets = "control-plane" }
      "ingress"       = { size = 2, subnets = "ingress", load_balancers = { "www" = [] } }
      "workers"       = { size = 10, subnets = "workers" }
    }
  }
}

module "shard_1b" {
  source  = "nstance-dev/nstance/aws//modules/shard"
  version = "~> 1.0"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard   = "us-east-1b"
  zone    = "us-east-1b"

  groups = {
    "default" = {
      "control-plane" = { size = 3, subnets = "control-plane" }
      "ingress"       = { size = 2, subnets = "ingress", load_balancers = { "www" = [] } }
      "workers"       = { size = 10, subnets = "workers" }
    }
  }
}

module "shard_1c" {
  source  = "nstance-dev/nstance/aws//modules/shard"
  version = "~> 1.0"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard = "us-east-1c"
  zone  = "us-east-1c"

  groups = {
    "default" = {
      "control-plane" = { size = 3, subnets = "control-plane" }
      "ingress"       = { size = 2, subnets = "ingress", load_balancers = { "www" = [] } }
      "workers"       = { size = 10, subnets = "workers" }
    }
  }
}
```

See the `examples/` directory for additional configurations including Google Cloud deployments and multi-cloud setups.

## Module Documentation

### Common Variables

All modules support these common variables for consistent naming and tagging:

| Variable | Description | Default |
|----------|-------------|---------|
| `name_prefix` | Prefix for all resource names: 2-23 lowercase letters, digits, or hyphens; starts with a letter and ends with a letter or digit | `"nstance"` |
| `tags` | Resource tags/labels (map of strings) | `{}` |

### Server Config Object

Each shard has a config file with server configuration in it. We support creating cluster-wide default configuration in the `cluster` module, and then expect to pass these down into each `shard` module invocation. Note that the `shard` module will overwrite select provider/account/region/zone-specific fields.

```hcl
# Nested objects matching ServerConfig structure
server_config = {
  request_timeout        = "30s"   # Request timeout
  create_rate_limit      = "100ms" # Duration between instance creates
  health_check_interval  = "60s"   # Expected agent health report interval
  default_drain_timeout  = "5m"    # Drain timeout before force delete (set to "0s" to disable Kubernetes drain coordination)
  image_refresh_interval = "6h"    # Image resolution refresh interval

  # Nested objects matching ClusterConfig structure

  cluster_leader_election = {
    frequent_interval   = "5s"  # Polling during cluster leader transitions
    infrequent_interval = "30s" # Polling during stable cluster leadership
    leader_timeout      = "15s" # Time before considering cluster leader failed
  }

  # Nested objects matching ShardConfig structure

  bind = {
    health_addr       = "0.0.0.0:8990"  # HTTP health endpoint bind address
    election_addr     = "0.0.0.0:8991"  # HTTPS leader election bind address
    registration_addr = "0.0.0.0:8992"  # gRPC registration service bind address
    operator_addr     = "0.0.0.0:8993"  # gRPC operator service bind address
    agent_addr        = "0.0.0.0:8994"  # gRPC agent service bind address
  }

  advertise = {
    health_addr       = ":8990"  # Advertised health address
    election_addr     = ":8991"  # Advertised election address
    registration_addr = ":8992"  # Advertised registration address
    operator_addr     = ":8993"  # Advertised operator address
    agent_addr        = ":8994"  # Advertised agent address
  }
  
  shard_leader_election = {
    frequent_interval   = "5s"  # Polling during shard leader transitions
    infrequent_interval = "30s" # Polling during stable shard leadership
    leader_timeout      = "15s" # Time before considering shard leader failed
  }

  garbage_collection = {
    interval                 = "2m"   # How often to run GC
    registration_timeout     = "5m"   # Wait for registration before terminating
    deleted_record_retention = "30m"  # Keep deleted records for
  }

  expiry = {
    eligible_age = ""  # Age for opportunistic expiry (e.g., "168h")
    forced_age   = ""  # Age for forced expiry (e.g., "720h")
    ondemand_age = ""  # Max age for on-demand instances
  }

  error_exit_jitter = {
    min_delay = "10s"  # Min delay before exit on error
    max_delay = "40s"  # Max delay before exit on error
  }
}
```

### Cluster Module

Generates shared cluster resources:
- Cluster ID (user-provided, lowercase alphanumeric with hyphens not leading/trailing/repeating, max 32 chars)
- S3/GCS bucket for config and state
- AWS Systems Manager Parameter Store as the default direct AWS secrets store
- Google Cloud Secret Manager as the default direct Google Cloud secrets store
- Optional encryption key in AWS Parameter Store, AWS Secrets Manager, or Google Cloud Secret Manager (only when `secrets_provider="object-storage"`)

**Key Variables:**
| Name | Description | Default |
|------|-------------|---------|
| `name_prefix` | Prefix for resource names | `"nstance"` |
| `cluster_id` | Cluster ID (required) | - |
| `shards` | Optional list of valid shard IDs for validation | `[]` |
| `bucket` | Existing S3/GCS bucket (if empty, a new bucket is created) | `""` |
| `versioning` | Enable object versioning on the bucket (increases storage costs) | `false` |
| `secrets_provider` | Secrets storage provider: `aws-parameter-store`, `object-storage` (encrypted in bucket), `aws-secrets-manager`, or `google-secret-manager` | Cloud-specific (`aws-parameter-store` on AWS; `google-secret-manager` on Google Cloud) |
| `secrets_prefix` | Explicit prefix for direct cloud secret names; when empty, derived from `name_prefix` | `""` |
| `encryption_key_provider` | Key source for object storage: `aws-parameter-store`, `aws-secrets-manager`, or `google-secret-manager` | Cloud-specific (`aws-parameter-store` on AWS; `google-secret-manager` on Google Cloud) |
| `encryption_key` | Existing encryption key source (AWS Parameter Store name or Secrets Manager ARN; Google Cloud secret name). Only used with `object-storage`; if empty, created. | `""` |
| `server_config` | Server configuration (if specified, merged over defaults) | `{}` |

Terraform always writes an effective `cluster.secrets.prefix` to generated shard configs. When `secrets_prefix` is empty, it derives `/<name_prefix>/` for AWS Parameter Store, `<name_prefix>/` for AWS Secrets Manager, or `<name_prefix>-` for Google Secret Manager. Explicit `secrets_prefix` and `encryption_key` values are passed through unchanged. When Terraform creates an object-storage encryption key, its name follows `name_prefix`.

**Outputs:**
| Name | Description |
|------|-------------|
| `id` | Cluster ID |
| `name_prefix` | Name prefix for resources |
| `shards` | List of valid shard IDs |
| `bucket` | S3 bucket name (AWS) or GCS bucket name (Google Cloud) |
| `bucket_arn` | S3 bucket ARN (AWS only) |
| `secrets_provider` | Secrets storage provider |
| `encryption_key_source` | Encryption key source identifier for the secrets store |
| `server_config` | Server configuration (defaults merged with user overrides) |

### Account Module

Creates IAM roles/service accounts:
- Server role with EC2, S3, ELB, and permissions for the selected Parameter Store or Secrets Manager provider
- Agent role with minimal EC2 describe permissions
- Instance profiles (AWS)

**Key Variables:**
| Name | Description | Default |
|------|-------------|---------|
| `cluster` | Cluster module output | - |
| `enable_ssm` | Enable SSM access (AWS) | `true` |

**Outputs:**
| Name | Description |
|------|-------------|
| `server_iam_role_arn` | Server IAM role ARN (AWS) or service account email (Google Cloud) |
| `agent_iam_role_arn` | Agent IAM role ARN (AWS) or service account email (Google Cloud) |
| `server_instance_profile_arn` | Server instance profile ARN (AWS only) |
| `agent_instance_profile_arn` | Agent instance profile ARN (AWS only) |

### Network Module

Creates VPC/network infrastructure:
- VPC with specified CIDR
- Internet Gateway
- NAT Gateway / Cloud NAT
- Route tables
- VPC Endpoints (S3, SSM) on AWS
- Group subnets (optional, via `subnets` variable)

**Key Variables:**
| Name | Description | Default |
|------|-------------|---------|
| `cluster` | Cluster module output (required) | - |
| `vpc_id` | Existing VPC ID (if set, skips VPC/IGW creation) | `""` |
| `vpc_cidr_ipv4` | VPC IPv4 CIDR block (required when creating new VPC, must be empty when using existing) | `""` |
| `enable_ipv6` | Enable IPv6 dual-stack support | `true` |
| `enable_ssm` | Create SSM VPC endpoints (AWS) | `true` |
| `subnets` | Subnet definitions by role key and zone (see below) | `{}` |
| `load_balancers` | Load balancer definitions (see below) | `{}` |

Shard validation uses `cluster.shards` - define valid shard IDs in the cluster module.

**Subnets Variable Structure:**

Each subnet definition supports the following attributes:

| Attribute | Description |
|-----------|-------------|
| `ipv4_cidr` | IPv4 CIDR block to create a new subnet |
| `ipv6_netnum` | Subnet number for auto-computed IPv6 /64 (AWS: 0-255, Google Cloud: 0-65535) |
| `ipv6_cidr` | Explicit IPv6 CIDR block (alternative to `ipv6_netnum`) |
| `existing` | Reference an existing subnet by ID (mutually exclusive with `ipv4_cidr`) |
| `public` | (bool) Route via Internet Gateway, assign public IPs |
| `nat_gateway` | (bool) Place a NAT gateway in this subnet |
| `nat_subnet` | (string) Route outbound traffic via NAT gateway in this role (same AZ) |
| `shards` | (list) Restrict subnet to specific shard IDs |

**Routing Behavior:**

- `public = true` → Routes via Internet Gateway, instances get public IPs
- `nat_subnet = "X"` → Routes via NAT gateway placed in role X's subnet (same AZ)
- Neither → Isolated subnet with user-managed routing

Routing fields (`public`, `nat_subnet`) work on both new AND existing subnets.

```hcl
subnets = {
  # Public subnet with NAT gateway
  "public" = {
    "us-west-2a" = [{
      ipv4_cidr   = "172.18.0.0/24"
      public      = true
      nat_gateway = true
    }]
  }
  # Private subnet routing through NAT
  "private" = {
    "us-west-2a" = [{
      ipv4_cidr  = "172.18.10.0/24"
      nat_subnet = "public"  # Routes via NAT in "public" role (same AZ)
    }]
  }
  # Existing subnet with NAT routing
  "existing-private" = {
    "us-west-2a" = [{
      existing   = "subnet-abc123"
      nat_subnet = "public"  # Can add routing to existing subnets
    }]
  }
  # Isolated subnet (no routing)
  "isolated" = {
    "us-west-2a" = [{ existing = "subnet-db123" }]
  }
  # Shard-specific subnet
  "workers" = {
    "us-west-2a" = [{
      ipv4_cidr  = "172.18.20.0/24"
      nat_subnet = "public"
      shards     = ["us-west-2a-1"]  # Only available to this shard
    }]
  }
}
```

When `shards` variable is specified, the network module validates that all shard IDs in subnet `shards` filters are in the allowed list.

**Load Balancers Variable Structure:**

Each load balancer definition supports the following attributes:

| Attribute | Description |
|-----------|-------------|
| `listeners` | (list of objects) External `port` and optional `target_port`, which defaults to `port` |
| `subnets` | (string) Subnet role key from the `subnets` variable |
| `public` | (bool, required) Whether the LB is internet-facing (`true`) or internal (`false`) |

On AWS, public load balancers require public subnets (with IGW routes). The module validates this at plan time.

```hcl
load_balancers = {
  # Public load balancer on ports 80 and 443, placed in ingress subnets
  "www" = {
    listeners = [{ port = 80 }, { port = 443 }]
    subnets = "ingress"
    public  = true
  }
  # Internal load balancer for API traffic
  "api" = {
    listeners = [{ port = 8080, target_port = 8081 }]
    subnets = "workers"
    public  = false
  }
}
```

Instances are registered with load-balancer listeners through the shard module's group `load_balancers` map. Keys select named load balancers; values select listener ports, and an empty list selects all listeners.

For example, `{ "www" = [] }` selects every `www` listener, while `{ "www" = [443] }` selects only its port 443 listener.

**Provider Differences:**

| Feature | AWS | Google Cloud |
|---------|-----|-----|
| NAT Gateway | Per-AZ (one NAT gateway per AZ for HA) | Regional (Cloud NAT covers all subnets) |
| Route Tables | Per-AZ private route tables | Not applicable (Cloud Router handles) |
| Public Subnets | Route via IGW, public IPs assigned | Marked for reference (load balancer placement) |

**Outputs:**
| Name | Description |
|------|-------------|
| `vpc_id` | VPC ID (AWS) or network self_link (Google Cloud) |
| `vpc_cidr_ipv4` | VPC IPv4 CIDR block |
| `vpc_cidr_ipv6` | VPC IPv6 CIDR block (null if disabled) |
| `public_subnet_ids` | Map of AZ/zone → subnet ID/name for public subnets |
| `nat_gateway_ids` | Map of AZ → NAT gateway ID (AWS) or `{"regional": name}` (Google Cloud) |
| `private_route_table_ids` | Map of AZ → route table ID (AWS only, empty for Google Cloud) |
| `subnet_ids` | Map of all managed subnet IDs by key (role key/zone/index) |
| `subnets` | Subnet metadata by role/zone with {id, shards, public} for each subnet |
| `load_balancers` | Map of LB name → {dns_name, arn, target_group_arns} (AWS) or {ip_address, instance_groups} (Google Cloud) |

### Shard Module

Deploys a single shard:
- Security groups / firewall rules
- Server instances
- Shard config (S3/GCS object)
- Load balancer (optional)

Note: All subnets (server and groups) are created by the network module and accessed via `var.network.subnets`.
The shard module filters subnets internally based on `shard` and `zone`.

When `cluster.shards` is non-empty, the shard module validates that `var.shard` is in the list.

**Key Variables:**
| Name | Description | Default |
|------|-------------|---------|
| `cluster` | Cluster module output | - |
| `account` | Account module output | - |
| `network` | Network module output (includes `subnets` with metadata) | - |
| `shard` | Unique shard identifier (must be in `cluster.shards` if set) | - |
| `zone` | Availability zone | - |
| `server_subnet` | Subnet role key from `network.subnets` for server instances | `"nstance"` |
| `dynamic_subnet_pools` | List of subnet pools allowed for dynamic groups (empty = all) | `[]` |
| `groups` | Map of group configurations (each group references a role from network subnets) | - |
| `templates` | Instance templates (if empty, uses default; if specified, used as-is) | `{}` |

**Subnet Filtering:**

The shard module automatically filters `var.network.subnets` to include only subnets that:
1. Are in the shard's zone
2. Either have no `shards` filter (shared) or include the shard's `shard` (isolated)

If no subnets are found after filtering, a validation error is raised (catches shard/zone typos).

**Outputs:**
| Name | Description |
|------|-------------|
| `shard` | The shard ID |
| `zone` | The zone for this shard |
| `server_ips` | List of server private IPs |
| `server_ids` | List of server instance IDs |
| `config_key` | S3/GCS key for shard config |
| `nlb_dns` | Load balancer DNS name (if enabled) |

## Architecture

```
┌───────────────────────────────────────────────────────────────────────────────────────────────┐
│                                   VPC (from network module)                                   │
│                                                                                               │
│                                      Internet Gateway                                         │
│                                             │                                                 │
│                    ┌────────────────────────┴────────────────────────┐                        │
│                    ▼                                                 ▼                        │
│  ┌────────────────────────────────────┐    ┌────────────────────────────────────┐             │
│  │   Public Subnet (AZ-A)             │    │   Public Subnet (AZ-B)             │             │
│  │  ┌──────────────────────────────┐  │    │  ┌──────────────────────────────┐  │             │
│  │  │   NAT Gateway (AZ-A)         │  │    │  │   NAT Gateway (AZ-B)         │  │             │
│  │  └──────────────────────────────┘  │    │  └──────────────────────────────┘  │             │
│  └────────────────────────────────────┘    └────────────────────────────────────┘             │
│                    │                                                 │                        │
│                    ▼                                                 ▼                        │
│  ┌────────────────────────────────────┐    ┌────────────────────────────────────┐             │
│  │   Shard Module A (AZ-A)            │    │   Shard Module B (AZ-B)            │             │
│  │  ┌──────────────────────────────┐  │    │  ┌──────────────────────────────┐  │             │
│  │  │   Server Subnet (Private)    │  │    │  │   Server Subnet (Private)    │  │             │
│  │  │  ┌────────────────────────┐  │  │    │  │  ┌────────────────────────┐  │  │             │
│  │  │  │ nstance-server         │  │  │    │  │  │ nstance-server         │  │  │             │
│  │  │  └────────────────────────┘  │  │    │  │  └────────────────────────┘  │  │             │
│  │  └──────────────────────────────┘  │    │  └──────────────────────────────┘  │             │
│  │                                    │    │                                    │             │
│  │  ┌──────────────────────────────┐  │    │  ┌──────────────────────────────┐  │             │
│  │  │   Group Subnets (Private)    │  │    │  │   Group Subnets (Private)    │  │             │
│  │  │  ┌────────────────────────┐  │  │    │  │  ┌────────────────────────┐  │  │             │
│  │  │  │ nstance-agent          │  │  │    │  │  │ nstance-agent          │  │  │             │
│  │  │  │ (provisioned by server)│  │  │    │  │  │ (provisioned by server)│  │  │             │
│  │  │  └────────────────────────┘  │  │    │  │  └────────────────────────┘  │  │             │
│  │  └──────────────────────────────┘  │    │  └──────────────────────────────┘  │             │
│  └────────────────────────────────────┘    └────────────────────────────────────┘             │
└───────────────────────────────────────────────────────────────────────────────────────────────┘
                                              │
                                              ▼
                          ┌─────────────────────────────────────┐
                          │   Cluster Module (shared storage)   │
                          │  S3/GCS, Parameter/Secrets Manager  │
                          └─────────────────────────────────────┘
```

## Tearing Down Infrastructure

S3/GCS buckets are protected from accidental deletion. With `force_destroy` unset (the default), `tofu destroy` will fail on non-empty buckets. If `versioning` is enabled, versioned objects must also be removed first.

**Preserve state when deleting a cluster (recommended for reprovisioning):**

```bash
# 1. Destroy the nstance-server instances to stop them from managing instances
# AWS:
tofu destroy -target=module.shard.aws_autoscaling_group.server
# Google Cloud:
tofu destroy -target=module.shard.google_compute_instance_group_manager.server

# 2. Terminate any remaining Nstance-managed instances (nstance-server provisions these outside of OpenTofu)

# AWS:
INSTANCE_IDS=$(aws ec2 describe-instances \
  --filters "Name=tag:nstance:managed,Values=true" "Name=tag:nstance:cluster-id,Values=<cluster-id>" "Name=instance-state-name,Values=running,stopped,pending" \
  --query 'Reservations[].Instances[].InstanceId' --output text)
if [ -n "$INSTANCE_IDS" ]; then
  aws ec2 terminate-instances --instance-ids $INSTANCE_IDS
  aws ec2 wait instance-terminated --instance-ids $INSTANCE_IDS
fi

# Google Cloud:
gcloud compute instances list \
  --filter="labels.nstance-managed=true AND labels.nstance-cluster-id=<cluster-id>" \
  --format="value(name,zone)" | while read NAME ZONE; do
    gcloud compute instances delete "$NAME" --zone="$ZONE" --quiet
  done

# 3. Destroy remaining compute and networking (keeps bucket and secrets intact)
tofu destroy -target=module.account -target=module.network
```

**Full teardown including deleting cluster state (bucket and secrets):**

```bash
# 1. Destroy the nstance-server instances to stop them from managing instances
# AWS:
tofu destroy -target=module.shard.aws_autoscaling_group.server
# Google Cloud:
tofu destroy -target=module.shard.google_compute_instance_group_manager.server

# 2. Terminate any remaining Nstance-managed instances

# AWS:
INSTANCE_IDS=$(aws ec2 describe-instances \
  --filters "Name=tag:nstance:managed,Values=true" "Name=tag:nstance:cluster-id,Values=<cluster-id>" "Name=instance-state-name,Values=running,stopped,pending" \
  --query 'Reservations[].Instances[].InstanceId' --output text)
if [ -n "$INSTANCE_IDS" ]; then
  aws ec2 terminate-instances --instance-ids $INSTANCE_IDS
  aws ec2 wait instance-terminated --instance-ids $INSTANCE_IDS
fi

# Google Cloud:
gcloud compute instances list \
  --filter="labels.nstance-managed=true AND labels.nstance-cluster-id=<cluster-id>" \
  --format="value(name,zone)" | while read NAME ZONE; do
    gcloud compute instances delete "$NAME" --zone="$ZONE" --quiet
  done

# 3. Destroy remaining infrastructure except the bucket
tofu destroy -target=module.account -target=module.network

# 4. Force-delete the bucket (including all object versions and delete markers)

# AWS:
BUCKET_NAME=$(tofu state show 'module.cluster.aws_s3_bucket.nstance[0]' | awk -F'"' '/^[[:space:]]*bucket[[:space:]]*=/ { print $2 }')
aws s3 rb "s3://${BUCKET_NAME}" --force
tofu state rm 'module.cluster.aws_s3_bucket.nstance[0]'

# Google Cloud:
BUCKET_NAME=$(tofu state show 'module.cluster.google_storage_bucket.nstance[0]' | awk -F'"' '/^[[:space:]]*name[[:space:]]*=/ { print $2 }')
gcloud storage rm -r "gs://${BUCKET_NAME}"
tofu state rm 'module.cluster.google_storage_bucket.nstance[0]'

# 5. Destroy remaining resources
tofu destroy
```

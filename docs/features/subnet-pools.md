---
title: "Subnet Pools"
weight: 90
description: "Logical subnet pool system that maps human-readable names to provider-specific subnet IDs for portable group configurations."
---

# Subnet Pools

This document describes how subnets are configured and used in Nstance.

## Overview

Nstance uses a logical subnet pool system that maps customisable and human-readable names to provider-specific subnet IDs. This abstraction allows templates and groups to reference subnets by subnet pool rather than provider-specific subnet ID. One benefit of this approach is that group configurations (and therefore, Kubernetes manifests) become portable across shards / providers / environment deployments.

## Configuration

### Server Subnet Pools Map

The `server.subnet_pools` configuration maps subnet pools to provider subnet IDs:

```jsonc
{
  "server": {
    "subnet_pools": {
      "control-plane": ["subnet-12345678"],           // Single subnet
      "workers": ["subnet-87654321", "subnet-abcdef"], // Multiple subnets for capacity
      "ingress": ["subnet-23456789"]
    },
    "dynamic_subnet_pools": ["workers"] // Optional: Restrict dynamic groups to these keys
  }
}
```

**Important**: The values are **provider subnet IDs**, not CIDR blocks:
- AWS: `subnet-12345678` (VPC subnet IDs)
- Google Cloud: Subnet self-links or names
- Proxmox: `vmbr0` (bridge names)

### Dynamic Subnet Pools

The optional `dynamic_subnet_pools` field restricts which subnet pools can be used by dynamic groups (created via the Operator API). If empty, any subnet pool from `server.subnet_pools` is allowed.

### Template and Group References

Templates and groups reference subnets pools by ID:

```jsonc
{
  "templates": {
    "worker": {
      "kind": "knd",
      "arch": "arm64",
      "subnet_pool": "workers"  // References subnet pool from server.subnet_pools
    }
  },
  "groups": {
    "default": {
      "apps": {
        "template": "worker",
        "size": 3,
        "subnet_pool": "workers"  // Can override template's subnet pool
      }
    }
  }
}
```

## Resolution Flow

When an instance is created, the subnet pool is resolved to provider subnet IDs:

```
1. Determine subnet pool ID
   └── Group.Subnets overrides Template.Subnets

2. Resolve subnet pool to a set of provider subnet IDs
   └── config.ResolveSubnetKey("workers") → ["subnet-87654321", "subnet-abcdef"]

3. Select subnet with capacity
   └── Iterate through IDs, call provider.CheckSubnetCapacity() on each
   └── Return first subnet with available capacity (>10 IPs for AWS)

4. Pass to provider
   └── provider.CreateInstance() receives single subnet ID
   └── e.g., EC2 RunInstances with SubnetId parameter
```

## Subnet Capacity Checking

Before creating an instance, Nstance checks that the target subnet has available IP addresses. This prevents failures due to subnet exhaustion.

For AWS, a subnet is considered to have capacity if it has more than 10 available IP addresses. This threshold provides a buffer for concurrent instance creation when multiple shards share a subnet. This check is in `internal/server/infra/aws/subnet.go` for reference.

When multiple subnets are configured for a subnet pool, Nstance iterates through them in order and uses the first one with available capacity.

## Validation

At configuration load time, Nstance validates:

1. `server.subnet_pools` must have at least one subnet pool
2. Each subnet pool must have at least one provider subnet ID
3. Provider subnet IDs cannot appear in multiple subnet pools (no overlaps)
4. `dynamic_subnet_pools` entries must reference existing subnet pools in `server.subnet_pools` if specified
5. Template and group subnet pool ID references must exist in `server.subnet_pools`

## Subnet Sharing Across Shards

Multiple shards in the same availability zone can share the same subnets.

**When to Share Subnets**

The typical Nstance deployment has one shard per availability zone. However, you may deploy multiple shards in the same AZ for:

- Blast radius reduction:
  Isolating failure domains so a shard issue doesn't affect all instances in the zone.

- Scale limits:
  Splitting large deployments when a single shard approaches operational limits for its nstance-server.
  Note that nstance-server is designed to be vertically-scaled only, and once that limit has been reached, the recommended approach is to simply scale out the number of shards.

In these cases, shards share the same subnet pools because:

1. Subnets represent network topology and are split by purpose, not by shard.
2. The capacity checking system (>10 available IPs) handles contention between shards.
3. Group configurations remain portable — a "workers" subnet pool ID works identically across shards.

**Example: Two Shards Sharing Subnets**

```jsonc
// Shard A config (config/us-west-2a-1.jsonc)
{
  "server": {
    "subnet_pools": {
      "workers": ["subnet-aaa111", "subnet-aaa222"],
      "control-plane": ["subnet-bbb111"]
    }
  }
}

// Shard B config (config/us-west-2a-2.jsonc) - same subnets
{
  "server": {
    "subnet_pools": {
      "workers": ["subnet-aaa111", "subnet-aaa222"],
      "control-plane": ["subnet-bbb111"]
    }
  }
}
```

Both shards provision instances into the same subnets. When Shard A scales up, it checks capacity and selects `subnet-aaa111` or `subnet-aaa222`. Shard B does the same independently—if `subnet-aaa111` is exhausted, both shards will use `subnet-aaa222`.

**Isolation Model**

When using Subnet sharing, it means shards provide compute isolation and not network isolation:

- Instances from different shards share the same subnets, route tables, and NACLs
- Security groups (not subnets) should be considered as alternative network isolation primitive
- If you require network-level isolation between shards, use separate subnets per shard

## Kubernetes CRD Integration

The `NstanceShardGroup` and `NstanceMachinePool` CRDs include subnet configuration:

**Spec (desired state)**:
- `spec.subnetPool` - Logical subnet pool ID for new dynamic groups

**Status (observed state)**:
- `status.config.subnetPool` - Actual subnet pool ID being used by the group

For static groups (defined in server config), the subnet pool ID cannot be modified via the Operator API. The `status.isStatic` field indicates whether the group is backed by static server config.

## Terraform Integration

When using Terraform to deploy Nstance:

1. All subnets (public, server, and group subnets) are defined in the network module via the `subnets` variable
2. Each subnet can specify routing behavior via `public`, `nat_gateway`, and `nat_subnet` attributes
3. The network module creates subnets, NAT gateways, and route tables, then outputs metadata (`role -> zone -> [{id, shards, public}]`)
4. The shard module receives `module.network` and filters subnets based on `zone` and `shard_name`
5. Server instances use the subnet from the role specified in `server_subnet` key (defaults to `"server"`)

### Subnet Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `existing` | string | Reference an existing subnet ID. Mutually exclusive with `ipv4_cidr`. |
| `ipv4_cidr` | string | Create a new subnet with this CIDR. Mutually exclusive with `existing`. |
| `ipv6_cidr` | string | Optional IPv6 CIDR to assign if creating a new subnet with `ipv4_cidr`. |
| `public` | bool | Route via IGW, assign public IPs on launch. Default: `false`. |
| `nat_gateway` | bool | Place a NAT Gateway in this subnet. Requires `public = true`. Default: `false`. |
| `nat_subnet` | string | Route via NAT from this role key (same AZ), e.g., `nat_subnet = "public"`. |
| `shards` | list | Restrict to specific shards. Empty = all shards can use. |

### Routing Behavior

- `public = true`: Associates subnet with public route table (IGW route)
- `nat_subnet = "X"`: Associates with private route table routing to NAT gateway in role "X" for same AZ
- Neither: No route table association (isolated or user-managed)

The routing fields (`public`, `nat_subnet`) control routing behavior regardless of whether the subnet is new (`ipv4_cidr`) or existing (`existing`). This allows you to add NAT routing to existing subnets.

```hcl
module "network" {
  source = "github.com/nstance-dev/nstance//deploy/tf/network"
  
  vpc_cidr_ipv4 = "10.0.0.0/16"
  
  subnets = {
    # Public subnet with NAT gateway
    "public" = {
      "us-west-2a" = [{
        ipv4_cidr   = "10.0.0.0/28"
        public      = true
        nat_gateway = true
      }]
    }
    # Server subnet routes through NAT
    "server" = {
      "us-west-2a" = [{
        ipv4_cidr  = "10.0.1.0/28"
        nat_subnet = "public"
      }]
    }
    # Worker subnet routes through NAT
    "workers" = {
      "us-west-2a" = [{
        ipv4_cidr  = "10.0.10.0/24"
        nat_subnet = "public"
      }]
    }
    # Existing DB subnet - add NAT routing
    "db" = {
      "us-west-2a" = [{
        existing   = "subnet-db-12345"
        nat_subnet = "public"
      }]
    }
  }
}

module "shard" {
  source = "github.com/nstance-dev/nstance//deploy/tf/shard"
  
  network    = module.network
  shard_name = "us-west-2a"
  zone       = "us-west-2a"
  # server_subnet defaults to "server"
  
  groups = {
    "workers" = { size = 5, subnet_pool = "workers" }
  }
}
```

## Provider-Specific Notes

### AWS

- Subnet IDs are EC2 VPC subnet IDs (e.g., `subnet-12345678`)
- Capacity checking uses `DescribeSubnets` API to get `AvailableIpAddressCount`
- Subnets should be in the same availability zone as the shard

### Google Cloud

- Subnet IDs are subnet names (e.g., `workers-subnet`)
- The nstance-server constructs the full resource path as `projects/{project}/regions/{region}/subnetworks/{subnet}` using the project ID and region from provider config
- Subnets must be in the same project and region as the shard

### Proxmox

- Subnet IDs are bridge names (e.g., `vmbr0`)
- No capacity checking is performed (bridges don't have IP limits)

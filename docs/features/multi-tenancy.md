---
title: "Multi-Tenancy"
weight: 80
description: "How multiple Kubernetes clusters can run on a single Nstance cluster with tenant isolation."
---

# Multi-Tenancy

This document describes how multiple Kubernetes clusters can share the same Nstance cluster/infrastructure with isolation.

Multi-tenancy provides tenant isolation: tenant identity scopes all resources and API access. Each tenant's operator connects to shared Nstance servers with a tenant-scoped certificate, and all groups, instances, and storage are isolated by tenant. If you don't need multi-tenancy, the `default` tenant provides expected single-tenant behavior.

## The Problem

Nstance was originally designed with a 1:1 mapping: one Nstance cluster per Kubernetes cluster. Each Nstance cluster has its own object storage bucket, its own CA, its own server configuration. This works, but it doesn't scale well when you want to run multiple Kubernetes clusters on shared physical infrastructure.

Consider you have two datacentres, each running a Proxmox cluster. You want to run three Kubernetes clusters—dev, staging, and production—spread across both zones for redundancy. Under the original design, you'd need six Nstance server deployments: one per (environment, zone) pair. Each would manage its own slice of the hardware, with no awareness of the others.

Multi-tenancy solves this by letting multiple Kubernetes clusters share the same Nstance servers. One Nstance cluster manages all three environments.

## Terminology

A **tenant** can be thought of as a Kubernetes cluster ("thought of" because Nstance supports an nstance-admin operator for use outside of Kubernetes setups). Each tenant has its own Nstance Operator running in its cluster, connecting to the shared Nstance servers. The tenant identity is embedded in the Operator's registration nonce and certificate. Note that each tenant can have multiple operator registrations to support rotation.

**Total instances** for a tenant means all instances belonging to that tenant, including both managed instances (created by group reconciliation) and on-demand instances (created via Operator API). This count includes all instances that consume host resources—any VM that exists on the provider (pending, running, stopping, draining, etc.). Only terminated/deleted VMs are excluded.

## Design Goals

1. **Tenant isolation**: Tenants cannot see or modify each other's groups, instances, or configuration.

2. **Cluster autoscaler compatibility**: The Kubernetes cluster autoscaler should work normally.

## Tenant Configuration

Tenant configuration is defined in the server configuration. Configuration is per-shard, meaning different shards can have different tenant configurations if needed.

Currently, tenant configuration is empty - the configuration struct is reserved for future functionality.

The `default` tenant cannot be defined in tenant configuration as it is a reserved special case.
Every operator must specify a tenant. If you don't need multi-tenancy, use the reserved tenant ID `default`. 

```jsonc
{
  "server": {
    "cluster_id": "example-cluster",
    "shard": "dc1-zone-a",
    // ... existing server config
  },

  "tenants": {
    // "default" is implicit
    // Do not define "default" here - it is reserved
    "prod": {},
    "staging": {},
    "dev": {}
  },

  // ... templates, groups, etc.
}
```

### Tenant IDs

Tenant IDs follow the same format as all other identifiers (shard ID, group ID, etc.): lowercase alphanumeric characters and hyphens, no leading/trailing/consecutive hyphens, maximum 32 characters. The ID `default` is reserved and cannot be used in the tenants configuration block (though valid in the groups configuration block).

### Group-Level Configuration

Groups are nested under their tenant in the config. This structure applies to both static groups (in server config) and dynamic groups (in object storage):

```jsonc
"groups": {
  "prod": {
    "control-plane": { 
      "template": "knc",
      "size": 3
    },
    "ingress": { 
      "template": "knd",
      "size": 2
    }
  },
  "dev": {
    "workers": { 
      "template": "knd",
      "size": 20
    },
    "batch": { 
      "template": "knd",
      "size": 10
    }
  },
  "default": {
    "standalone": {
      "template": "knd",
      "size": 5
    }
  }
}
```

The tenant is implicit from the parent key — no `tenant` field is needed on each group. Group names only need to be unique within their tenant; different tenants can have groups with the same name as groups in other tenants.

For dynamic groups created via the Operator API, the tenant is determined from the operator's certificate and groups are stored under the corresponding tenant key in `groups/{shard}.jsonc`.

## Tenant Identity

Every API client has a tenant identity. For Operators, this comes from the registration nonce JWT. For Agents, the tenant is inherited from the group that created the instance.

### Registration Nonce Claims

The registration nonce JWT includes a tenant claim:

```json
{
  "sub": "example-cluster",
  "kind": "operator",
  "cluster_id": "example-cluster",
  "tenant": "prod",
  "exp": 1234567890
}
```

The `tenant` claim identifies which tenant applies to this operator. The server validates that the tenant is either `default` or exists in the server configuration before issuing a certificate.

### Client Certificate

The operator's client certificate includes the tenant in the standard Organization (O) field:

```
Subject: CN=example-cluster, O=prod
```

This allows the server to extract tenant identity from every authenticated request without additional lookups. The server validates that certificates contain exactly one O value; certificates with zero or multiple O values are rejected during authentication.

### Agent Certificates

Agent certificates include the tenant in the Organization (O) field, same as operators. The server includes the tenant when generating the agent's registration nonce JWT, and embeds it in the certificate at registration time.

## Storage Scoping

All persistent state is scoped by tenant.

### Object Storage Layout

The object storage layout remains largely unchanged. Files that need tenant identification are prefixed with the tenant ID:

```
bucket/
  leader/
    cluster.json
    {shard}.json
  config/
    {shard}.jsonc
  groups/
    {shard}.jsonc
  secret/
    ca.key
    service-accounts.key
  operator/
    {tenant}.{storage-key}.json
  instance/
    {shard}/
      {tenant}.{instance-id}.json
  certlog/
    {shard}/
      {tenant}.{timestamp}.{instance-id}.json
```

Note: The period delimiter is unambiguous because tenant IDs, instance IDs, and timestamps cannot contain periods (only lowercase alphanumeric and hyphens).

The tenant prefix makes it easy to identify which tenant owns a record when browsing object storage directly. Tenant isolation is enforced at the API layer - the server validates tenant identity from the client certificate before any read or write.

**Summary of object storage changes:**

| Path | Before | After |
|------|--------|-------|
| `operator/` | `{storage-key}.json` | `{tenant}.{storage-key}.json` |
| `instance/{shard}/` | `{instance-id}.json` | `{tenant}.{instance-id}.json` |
| `certlog/{shard}/` | `{timestamp}-{instance-id}.json` | `{tenant}.{timestamp}.{instance-id}.json` |

Note: The certlog format also changes its delimiter from hyphen to period for consistency with the new period-delimited convention.

### SQLite Schema Changes

The groups table gains a tenant column:

```sql
CREATE TABLE groups (
  tenant TEXT NOT NULL,
  group_key TEXT NOT NULL,
  runtime_config_hash TEXT NOT NULL,
  infra_config_hash TEXT NOT NULL,
  PRIMARY KEY (tenant, group_key)
);
```

The instances table gains a tenant column:

```sql
CREATE TABLE instances (
  id TEXT PRIMARY KEY,
  tenant TEXT NOT NULL,
  group_key TEXT NOT NULL,
  -- ... existing columns
);

CREATE INDEX idx_instances_tenant ON instances(tenant);
CREATE INDEX idx_instances_tenant_group ON instances(tenant, group_key);
```

## API Scoping

All Operator API methods are scoped by the caller's tenant identity.

### ListGroups

Returns only groups belonging to the calling tenant.

```go
func (s *Service) ListGroups(ctx context.Context, req *emptypb.Empty) (*proto.ListGroupsResponse, error) {
    clientInfo, _ := api.GetClientInfo(ctx)
    tenant := clientInfo.Tenant  // extracted from certificate
    
    groups, err := s.listGroupsForTenant(tenant)
    // ...
}
```

### UpsertGroup

Creates or updates a group for the calling tenant.

```go
func (s *Service) UpsertGroup(ctx context.Context, req *proto.UpsertGroupRequest) (*proto.GroupStatus, error) {
    clientInfo, _ := api.GetClientInfo(ctx)
    tenant := clientInfo.Tenant
    
    // Proceed with upsert
    // ...
}
```

### Watch Streams

`WatchGroups`, `WatchInstances`, and `WatchErrors` are all filtered by tenant. An operator only receives events for its own groups and instances.

## Cluster Autoscaler Integration

The Kubernetes cluster autoscaler works normally with multi-tenancy. The Nstance Operator translates replica changes to `UpsertGroup` calls.


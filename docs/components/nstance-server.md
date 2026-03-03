---
title: "Nstance Server"
weight: 10
description: "The core control plane component responsible for configuration management, leader election, instance provisioning, health monitoring, and API services."
---

# Nstance Server

The `nstance-server` is the core control plane component of the Nstance [architecture](../architecture.md). It manages instance lifecycle, configuration, health monitoring, and provides gRPC API services for agents and operators.

Each server is assigned to a zone **shard** - a logical partition of infrastructure typically mapped to a cloud availability zone. The leader election mechanism allows for multiple server deployments per shard, enabling hot standby's to exist for fast failover.

## CLI Flags

Most of the server configuration is defined in [Configuration](#configuration) files, however in order to retrieve those files from object storage a minimal set of CLI flags exist.

| Flag | Default | Description |
|------|---------|-------------|
| `--debug`, `-v` | `false` | Enable debug logging |
| `--version` | `false` | Show version information |
| `--validate` | *(optional)* | Validate configuration and exit (see [Validate Mode](#validate-mode)) |
| `--id` | *(required)* | Unique identifier for server instance in the cluster |
| `--shard` | *(required)* | Unique identifier for the shard the server belongs to |
| `--storage` | *(required)* | Object storage provider: `s3`, `gcs`, or `file` |
| `--bucket` | *(required)* | Object storage bucket for configuration and state |
| `--prefix` | `shard/{shard}/` | Key prefix for shard data in the bucket |
| `--cachedir` | `./cache` | Directory for cache and database files |
| `--advertise-host` | | Override advertise host for health and election addresses |

### Example

```bash
nstance-server \
  --id server-1 \
  --shard us-west-2a \
  --storage s3 \
  --bucket my-cluster-bucket
```

### Validate Mode

The `--validate` flag runs configuration validation and exits without starting the server. This is useful for CI pipelines and pre-deploy checks.

```bash
# Validate a local config file (no storage flags needed)
nstance-server --validate /path/to/config.jsonc

# Fetch and validate the shard config from object storage
nstance-server --shard us-west-2a --storage s3 --bucket my-bucket --validate
```

When a local file path is provided, the local file is loaded and validated.

When the flag is present with no value, the server fetches the shard config from object storage and validates it.

In both cases, the server exits after validation with a zero exit code on success or non-zero on failure.

## Configuration

Server configuration consists of two files per zone shard, stored in object storage:

1. **Static Configuration** (`config/{shard}.jsonc`) — Managed by infrastructure tooling (e.g., OpenTofu/Terraform). Defines the shard's infrastructure settings, instance templates, and initial group definitions. The server treats this file as read-only.

2. **Dynamic Groups** (`groups/{shard}.jsonc`) — Managed at runtime via the Operator API. Contains group overrides and dynamically created groups that are merged over the static configuration.

Configuration is in [JSONC](https://jsonc.org/) format (JSON with comments). See the [Server Config Reference](../reference/server-config.md) for the full schema and available options.

On startup, the server downloads both files from object storage, validates them, and syncs the merged result to a local cache. If the download or validation fails after retries, the server will not become healthy.

## Health Endpoint

The server exposes an HTTP health endpoint for integration with load balancers and auto-scaling group health checks.

| Setting | Value |
|---------|-------|
| **Bind address** | Configured via `server.bind.health` (default: `0.0.0.0:8990`) |
| **Protocol** | HTTP |
| **Paths** | `/health` and `/` |

**Responses:**
- `200 OK` — Server has successfully loaded configuration and is ready to serve.
- `503 Service Unavailable` — Server is still initializing or unhealthy.

The health endpoint starts listening immediately on boot but returns `503` until configuration loading completes. This allows auto-scaling groups to detect and replace instances that fail to initialize.

## gRPC API

The server exposes three gRPC services, each on a separate bind address configured in the [server config](../reference/server-config.md):

| Service | Authentication | Description |
|---------|---------------|-------------|
| **Registration** | Anonymous | Client registration for agents and operators. Exchanges registration nonce JWTs for client certificates. |
| **Agent** | mTLS (agent role) | Bidirectional API for agent operations: key generation, certificate issuance, health reporting, and file delivery. |
| **Operator** | mTLS (operator role) | API for operator operations: group management (create/update/delete), config refresh, and real-time group change notifications. |

Access to the Agent and Operator services is restricted by the role encoded in the client certificate.

## Leader Election

When multiple servers run in the same shard, one is elected as the **shard leader**. Only the leader performs active infrastructure management (provisioning, health checks, reconciliation). Non-leader servers remain on standby and can serve API requests.

Leader election coordination is done using object storage with [s3lect](../reference/leader-election.md), which requires no additional infrastructure beyond the object storage bucket already used for configuration.

See the [Leader Election Reference](../reference/leader-election.md) for details on how election works, leader failover, and configuration options.

## Startup Behavior

On boot, the server performs an atomic configuration load sequence before becoming healthy:

1. **Load Static Configuration**: Download `config/{shard}.jsonc` from object storage (or load from local cache if available), parse JSONC, and validate
2. **Load Dynamic Groups**: Download `groups/{shard}.jsonc` from object storage (or load from local cache), parse and validate
3. **Sync to SQLite Cache**: Compute runtime and infrastructure hashes for all merged groups and store in local SQLite database

All three steps are performed atomically — if any step fails (including validation), all steps are retried up to 3 times with exponential backoff (1s, 2s).

If loading fails after all retries, the server **hangs indefinitely** with a fatal error logged rather than exiting. This prevents restart loops that could cause excessive object storage reads and spike infrastructure costs. Because the health endpoint remains `503` until configuration loading completes, if desired an ASG can detect and rotate the instance automatically.

The SQLite sync step is critical because groups must exist in the database with computed hashes for [config drift detection](../reference/instance-lifecycle.md#config-hash-design) to work. SQLite also stores ephemeral but important state such as instance registrations, health reports, and drain coordination — without it, the server cannot operate correctly.

## Server API Reference

For detailed documentation of the server's gRPC API methods, see the [Server API Reference](../reference/server-api.md).

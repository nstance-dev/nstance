---
title: "Nstance Admin"
weight: 40
description: "Command-line and HTTP API tool for managing Nstance clusters, including bootstrap operations, configuration management, and group scaling."
---

# Nstance Admin

The `nstance-admin` tool provides command-line and HTTP API interfaces for managing Nstance clusters.

## Command Modes

The admin CLI has three modes of operation:

1. **Cluster commands** (`nstance-admin cluster ...`): Direct access to cluster storage and secrets. Used for bootstrap operations or when leader election is disabled.
2. **Shard commands** (`nstance-admin config ...`, `nstance-admin group ...`): Communicate with one or more nstance-servers via gRPC.
3. **HTTP API** (`nstance-admin serve`): Exposes all Shard commands as HTTP endpoints.

## Authentication

### For Cluster Commands

Cluster commands (`nstance-admin cluster nonce`, `nstance-admin cluster register-operator`) access storage and secrets directly. They require:
- Storage credentials (AWS/Google Cloud credentials or file system access)
- For `object-storage` secrets provider: an encryption key

### For Shard Commands

Shard commands require an operator identity that is pre-provisioned via `cluster register-operator`. This identity consists of:
- `ca.crt` - Cluster CA certificate
- `identity.key` - Private key
- `identity.crt` - Signed client certificate

The identity files are stored in `--identity-dir` (default: `<temp-dir>/cli-operator-identity/`).

## Bootstrap Workflow

Before using shard commands, you must bootstrap the operator identity:

### Step 1: Register Operator

Generate identity files using the `cluster register-operator` command:

```bash
nstance-admin cluster register-operator \
  --storage-bucket my-cluster-bucket \
  --secrets-provider object-storage \
  --key-provider env
```

This writes to `<temp-dir>/cli-operator-identity/`:
- `ca.crt` - Cluster CA certificate
- `identity.key` - Private key (auto-generated)
- `identity.crt` - Signed client certificate

Options:
- `--tenant <name>`: Tenant ID for the operator (default: `default`)
- `--output-dir <path>`: Write to a custom directory
- `--operator-id <id>`: Use a specific operator ID (auto-generates puidv7 with "opr" prefix if not provided)
- `--public-key-file <path>`: Use an existing public key instead of generating one
- `--cert-ttl-hours <hours>`: Certificate TTL in hours (default: 8760 = 1 year)

### Step 2: Run Shard Commands

Use the identity to communicate with shard servers:

```bash
# Refresh config on all shards
nstance-admin config refresh \
  --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" \
  --all-shards

# Scale group on specific shard
nstance-admin group scale my-group 5 \
  --servers "us-west-2a=172.16.0.1:8993" \
  --shard us-west-2a
```

## Cluster Commands

Cluster commands operate directly on storage and secrets, bypassing nstance-servers.

### Cluster Command Flags

All `nstance-admin cluster` commands share these persistent flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--storage-provider` | `s3` | Storage provider (s3, gcs, file) |
| `--storage-bucket` | *(required)* | Storage bucket name |
| `--storage-prefix` | `cluster/` | Storage prefix for cluster data |
| `--secrets-provider` | *(required)* | Secrets provider (object-storage, aws-parameter-store, aws-secrets-manager, google-secret-manager) |
| `--secrets-prefix` | `secret/` | Secrets prefix |
| `--secrets-project` | | Project ID (required for google-secret-manager) |
| `--key-provider` | | Encryption key provider (env, file, aws-parameter-store, aws-secrets-manager, google-secret-manager) - required for object-storage |
| `--key-source` | | Key source (environment variable, file, Parameter Store name, or secret ARN) - defaults to `NSTANCE_ENCRYPTION_KEY` for env provider, otherwise required |

`nstance-admin cluster` does not infer a cloud provider, so `--secrets-provider` must be specified explicitly. Pass `--secrets-prefix` with the same value as `cluster.secrets.prefix` in the Nstance Server configuration—for example, `/nstance/` for the default AWS Parameter Store configuration. Google Secret Manager also requires `--secrets-project`.

### `nstance-admin cluster nonce`

Generate a registration nonce JWT for the Nstance Operator.

```bash
nstance-admin cluster nonce --cluster-id <cluster-id> --storage-bucket <bucket> --secrets-provider <provider> [--key-provider <provider>] [--tenant <tenant>] [--expiry <duration>] [--output <path>]
```

**Command-specific flags:**
- `--cluster-id` (required): Cluster ID
- `--tenant`: Tenant identifier (default: `default`)
- `--expiry`: JWT expiry duration (default: `3h`)
- `--output`: Output path for nonce JWT (default: `<temp-dir>/cli-operator-identity/nonce.jwt`, use `-` for stdout)

**Example:**
```bash
# Generate nonce using the direct AWS Parameter Store backend
nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --secrets-provider aws-parameter-store --secrets-prefix /nstance/

# Generate nonce using encryption key from environment variable
nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --secrets-provider object-storage --key-provider env

# Generate nonce using AWS Secrets Manager for the encryption key
nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --secrets-provider object-storage --key-provider aws-secrets-manager --key-source arn:aws:secretsmanager:...

# Generate nonce using Parameter Store for the encryption key
nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --secrets-provider object-storage --key-provider aws-parameter-store --key-source /nstance/encryption-key

# Generate nonce and output to stdout (for Kubernetes secrets)
nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --secrets-provider object-storage --key-provider env --output -

# Generate nonce with custom tenant and expiry
nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --secrets-provider object-storage --key-provider env --tenant prod --expiry 1h
```

When using `--output -`, the JWT is written to stdout and can easily be stored in a Kubernetes Secret for operator bootstrap. Otherwise, the nonce is written to a file, great for use by subsequent admin commands.

---

### `nstance-admin cluster register-operator`

Register an operator with the cluster by creating a client certificate and storing the registration record.

This command is used to bootstrap operator identity for nstance-admin shard commands. It can also be used when cluster leader election is disabled on all nstance-servers and operators cannot be registered via gRPC.

```bash
nstance-admin cluster register-operator --storage-bucket <bucket> --secrets-provider <provider> [--key-provider <provider>]
```

**Command-specific flags:**
- `--tenant`: Tenant ID for the operator (default: `default`)
- `--operator-id`: Operator ID (auto-generates puidv7 with "opr" prefix if not provided)
- `--public-key-file`: Path to operator's public key file (PEM) - if not provided, generates a new keypair
- `--output-dir`: Directory to write identity files (default: `<temp-dir>/cli-operator-identity/`)
- `--cert-ttl-hours`: Certificate TTL in hours (default: 8760 = 1 year)

**Output files** (written to `--output-dir`):
- `ca.crt` - Cluster CA certificate
- `identity.key` - Private key (generated if `--public-key-file` not provided)
- `identity.crt` - Signed client certificate

**Example:**
```bash
# Register operator with auto-generated ID and keypair
nstance-admin cluster register-operator \
  --storage-bucket my-cluster-bucket \
  --secrets-provider object-storage \
  --key-provider env

# Register operator with custom output directory
nstance-admin cluster register-operator \
  --storage-bucket my-cluster-bucket \
  --secrets-provider object-storage \
  --key-provider env \
  --output-dir /path/to/identity

# Register operator with specific ID and existing public key
nstance-admin cluster register-operator \
  --storage-bucket my-cluster-bucket \
  --secrets-provider object-storage \
  --key-provider env \
  --operator-id opr01abc123... \
  --public-key-file /path/to/operator.pub
```

---

## Shard Commands

### Shard Command Flags

All shard commands (`config`, `group`) and the `serve` command share these flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--servers` | *(required)* | Shard servers (format: `shard1=host1:port1,shard2=host2:port2`) |
| `--identity-dir` | `<temp-dir>/cli-operator-identity/` | Directory containing identity files |
| `--shard` | | Target a specific shard |
| `--all-shards` | | Target all shards in the servers list |
| `--timeout` | `30s` | Timeout for operations |

---

### `nstance-admin config refresh`

Trigger a config file refresh from object storage on one or more Nstance Servers.

```bash
nstance-admin config refresh --servers "<servers>" --shard <shard>
nstance-admin config refresh --servers "<servers>" --all-shards
```

**Examples:**
```bash
# Refresh a single shard
nstance-admin config refresh \
  --servers "us-west-2a=172.16.0.1:8993" \
  --shard us-west-2a

# Refresh all shards
nstance-admin config refresh \
  --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" \
  --all-shards
```

Note: `--shard` and `--all-shards` are mutually exclusive.

**Behavior:**
1. Loads identity from `--identity-dir`
2. Connects to Server via gRPC Operator API
3. Calls `RefreshConfig()` RPC
4. Reports result: configuration updated or unchanged

---

### `nstance-admin config status`

Check configuration status on one or more Nstance Servers.

```bash
nstance-admin config status --servers "<servers>" --shard <shard>
nstance-admin config status --servers "<servers>" --all-shards
```

**Output:**
```
SHARD         ETAG                              LAST_MODIFIED              SIZE
us-west-2a    d41d8cd98f00b204e9800998ecf8427e  2024-01-15T10:30:00Z       4096
us-west-2b    a1b2c3d4e5f6789012345678abcdef00  2024-01-15T09:15:00Z       4128
```

---

### `nstance-admin serve`

Start an HTTP API server for remote administration.

```bash
nstance-admin serve --servers "<servers>" [--bind <addr>]
```

**Additional Flags:**
- `--bind`: Address to bind the HTTP server (default: `:8080`)

**Examples:**
```bash
# Start on default port
nstance-admin serve --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993"

# Start on specific address
nstance-admin serve \
  --servers "us-west-2a=172.16.0.1:8993" \
  --bind 127.0.0.1:9090
```

---

### `nstance-admin group scale`

Scale a group to a specified size.

```bash
nstance-admin group scale <group> <size> --servers "<servers>" --shard <shard>
nstance-admin group scale <group> <size> --servers "<servers>" --all-shards
```

**Arguments:**
- `<group>` (required): The group ID to scale
- `<size>` (required): The desired size (number of instances)

**Examples:**
```bash
# Scale a single shard
nstance-admin group scale my-group 5 \
  --servers "us-west-2a=172.16.0.1:8993" \
  --shard us-west-2a

# Scale on all shards
nstance-admin group scale my-group 10 \
  --servers "us-west-2a=172.16.0.1:8993,us-east-1a=172.16.0.2:8993" \
  --all-shards
```

Note: `--shard` and `--all-shards` are mutually exclusive.

**Behavior:**
1. Connects to the Nstance Server via gRPC Operator API
2. Calls `UpsertGroup()` RPC with only the size field set
3. For static groups (restricted editing): only updates unrestricted fields like size
4. For dynamic groups (unrestricted editing): updates the size field

---

## HTTP API

When running with `serve`, the following JSON endpoints are available:

### `GET /health`

Health check endpoint.

**Response:** `200 OK` with body `OK\n`

---

### `GET /config/status`

Get configuration status for one or more shards.

**Query Parameters:**
- `shard`: Target a specific shard
- `all_shards=true`: Target all shards

**Example:** `GET /config/status?shard=us-west-2a`

**Response:**
```json
{
  "shards": [
    {
      "shard": "us-west-2a",
      "etag": "d41d8cd98f00b204e9800998ecf8427e",
      "last_modified": "2024-01-15T10:30:00Z",
      "size": 4096
    }
  ]
}
```

---

### `POST /config/refresh`

Trigger configuration refresh on one or more shards.

**Query Parameters:**
- `shard`: Target a specific shard
- `all_shards=true`: Target all shards

**Example:** `POST /config/refresh?shard=us-west-2a`

**Response:**
```json
{
  "shards": [
    {
      "shard": "us-west-2a",
      "updated": true,
      "etag": "a1b2c3d4e5f6..."
    }
  ]
}
```

---

### `POST /group/scale`

Scale a group to a specified size.

**Query Parameters:**
- `shard`: Target a specific shard
- `all_shards=true`: Target all shards

**Request:**
```json
{
  "group": "my-group",
  "size": 5
}
```

**Example:** `POST /group/scale?shard=us-west-2a`

**Response:**
```json
{
  "results": [
    {
      "shard": "us-west-2a",
      "group": "my-group",
      "size": 5
    }
  ]
}
```

**cURL Example:** `curl -d '{"group": "test","size": 2}' -X POST http://localhost:8080/group/scale\?shard\=dev`

---

## Network Requirements

For shard commands, `nstance-admin` must be able to reach the Nstance Server operator endpoints directly. The endpoints are specified via the `--servers` flag.

For `serve` mode, the HTTP server can be exposed via a reverse proxy or tunnel (e.g., Cloudflare Tunnel) with zero-trust authentication for secure remote access from outside the VPC.

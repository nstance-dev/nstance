---
title: "Data Storage"
weight: 30
description: "Object storage layout, SQLite caching, and data persistence model."
---

# Data Storage

Nstance Server persistent state is stored in object storage (S3 or GCS), allowing for scale-to-zero support of the Nstance Server itself.

The bucket is divided into two scopes using key prefixes:

* **Cluster scope** (`cluster/`): Data shared across all shards — CA certificate, encrypted secrets, leader election, and operator registrations. Can optionally be a separate bucket.
* **Shard scope** (`shard/{shard}/`): Per-zone shard data — configuration, dynamic groups, leader election, instance records, and certificate logs.

Example bucket file structure (where `{shard}` might be `us-west-2a`):

```
bucket/
  cluster/
    ca.crt
    leader.json
    secret/
      ca.key
      registration-nonce.key
    operator/
      {tenant}.{storage-key}.json
  shard/{shard}/
    config.jsonc
    groups.jsonc
    leader.json
    instance/
      {tenant}.{storage-key}.json
    certlog/
      {tenant}.{timestamp}.{instanceID}.json
```

In the above example structure, where `{storage-key}` is the UUID-prefix format of a puidv7 ID (e.g., `01970a1c-e31e-7422-9cd5-e9651d11cc97-knc`):

* `cluster/ca.crt` is the CA certificate stored unencrypted in cluster-scoped storage, accessible to all shards.

* `cluster/leader.json` is the cluster [leader election](leader-election.md) lockfile (cross-shard), managed by the `s3lect` library.

* `cluster/secret/` contains encrypted secrets (encrypted using the Encryption Key), including the CA private key (`ca.key`), the registration nonce signing key (`registration-nonce.key`), and any custom secrets.

* `cluster/operator/` contains one file per registered Operator, keyed by tenant and storage key. Generally this should only contain one file per tenant, or a second if the Operator is in the process of rotating its private key.

* `shard/{shard}/config.jsonc` is the Nstance Server configuration file for this zone shard (JSONC format with comment support).

* `shard/{shard}/groups.jsonc` is the dynamic groups configuration for this zone shard (JSONC format).

* `shard/{shard}/leader.json` is the shard [leader election](leader-election.md) lockfile (within a single zone shard), managed by the `s3lect` library.

* `shard/{shard}/instance/` contains one file per registered VM instance Nstance Agent, keyed by tenant and storage key.

* `shard/{shard}/certlog/` contains the CA's certificate issuance log for this zone shard. Anytime the CA generates certificates for an Nstance Agent, it logs the certificate name, its serial number, and its expiry, in a single file keyed by tenant, a millisecond-precision UTC timestamp, and the instance ID.

Both the `operator` and `instance` files capture the public keys and client certificate serials and expiration dates.

The `instance` files also store special hashes of configuration, so that if configuration changes since the instance was provisioned, Nstance Server can either push configuration changes or begin rotating/updating instances - see [Push Updates & Instance Rotation](instance-lifecycle.md#push-updates--instance-rotation).

## SQLite for Ephemeral Data

To improve performance and keep costs in-check by minimising object storage read operations, Nstance Server uses a local SQLite database which exclusively stores ephemeral data of three distinct types:

1. **Object Storage-Restorable Data** (critical): Instance registration records (including public keys and certificate metadata), configuration hashes, etc.

2. **Provider-Restorable Data** (infrastructure): Instance hostnames, IP addresses, current status from cloud provider APIs, load balancer group registrations

3. **Time-based Ephemeral Data** (operational): Data which comes in at regular intervals and can be repopulated on the next interval e.g. latest health reports per instance.

It's important to note that any of these types of data can reference the same instance ID, and that provider data may exist where an instance has not yet been registered, while we may also insert an instance ID from object storage data prior to inserting it from provider data, but for time-based ephemeral data we will not insert it unless the object storage data has been restored.

### Load Balancer Registrations

The `lb_instances` table tracks instance registrations with configured load balancers:
- **Table**: `lb_instances` with fields: `lb_key`, `instance_id`, `status`, `updated_at`
- **Status**: `pending`, `registered`, `deregistered`, `failed`
- **Reconciliation**: Pending/failed registrations are retried on every health report for eventual consistency
- **Cache Warming**: On leader election, existing registrations are queried from provider APIs and cached

## Instance Creation Failure Recovery

Instance creation involves multiple steps with different durability guarantees:

1. Generate instance ID and registration nonce JWT (with cluster_id, shard, group, on_demand claims)
2. Write "pending" object storage record BEFORE provider call for durability (note: no provider_id yet)
3. Pre-insert to SQLite (local, ephemeral, no provider_id yet) - prevents GC race condition
4. Call provider to create VM
5. Update object storage record with provider_id (and IPs if available) immediately after provider call succeeds
6. Update SQLite with provider_id and IPs if available (local cache)
7. Agent registers → object storage + SQLite updated with public key, certificate metadata, registration timestamp, and authoritative IPs from agent

**Design Principle**: Write object storage record BEFORE provider call. This ensures:
- Registration can always find the object storage record to update
- Server crash/reschedule doesn't lose pending instance data
- Reconciler + GC both see pending instances (accurate group counts)

Also note that we eagerly capture VM IPs if available on create instance purely for debugging purposes (e.g. AWS is sync and returns IPs, vs Proxmox VE is async and returns empty values for IPs), but during instance registration we overwrite and treat those IPs as canonical.

**Recovery Scenarios:**

| Scenario | Storage State | VM State | Recovery |
|----------|---------------|----------|----------|
| Server dies after object storage write, before provider call | "pending" (no provider_id) | None | GC deletes object storage + SQLite after timeout, reconciler creates replacement |
| Server dies after provider call, before S3 provider_id write | "pending" (no provider_id) | Running with valid JWT | New server: seedFromStorage + seedFromProvider → adds provider_id → agent registers → **Instance recovered** |
| Server dies after S3 provider_id write, before SQLite update | "pending" (has provider_id) | Running with valid JWT | New server: seedFromStorage (has provider_id) → agent registers → **Instance recovered** |
| Server dies after SQLite update, before registration | "pending" (has provider_id) | Running | Same as above → **Instance recovered** |
| Provider call fails | "pending" (no provider_id) | None | GC deletes after timeout, reconciler creates replacement |
| JWT expires before registration | "pending" | Running with expired JWT | Agent registration fails, GC terminates VM + deletes object storage + SQLite |

**Recovery on new server startup:**

1. New server starts with fresh SQLite
2. `RebuildCache` runs:
   - `seedFromStorage`: populates SQLite from object storage (may lack provider_id)
   - `seedFromProvider`: upserts provider_id into SQLite by matching instance_id tag
3. Agent registers using JWT embedded in userdata
4. Registration succeeds (object storage record exists), updates object storage with provider_id and authoritative IPs

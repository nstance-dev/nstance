---
title: "Instance Lifecycle"
weight: 40
description: "Instance provisioning, registration, health monitoring, drain coordination, and deletion."
---

# Groups & Instances

Nstance Server schedules instances in two ways:

1. `Groups` are like traditional auto-scaling groups, with a target/desired capacity set on the group "size", which Nstance Server works to ensure is met through automatic reconciliation.

- `Static Groups` are defined in the Nstance Server configuration. Defining a group in server config **enables restricted editing** for that group.

- `Dynamic Groups` can be created via the Operator API, referencing a template defined in server config. Dynamic groups have **unrestricted editing**.

- Each `Group` has an ID.

- A Static Group can be partially overwritten by a Dynamic Group of the same key - but only the size, instance type, and vars can be overwritten (unrestricted fields) - not the instance template or subnet/s (restricted fields).

- Removing a group from server config **disables restricted editing**, allowing full control via the Operator API.

- A new Dynamic Group (with no matching static group ID) requires a template reference and optionally a subnet pool. If the subnet pool is not specified, the template's default subnet pool is used.

- This enables vertical and horizontal scaling, as well as functionality like Kubernetes node labelling (through vars), without the risk of key groups becoming broken (e.g. control plane nodes having their instance template changed).

    - Instances created for groups are called "managed instances" and are automatically scaled by the reconciler to maintain the desired group size.

2. `Instances` can be created on-demand, via the Operator API, and must reference a `Group`.

    - On-demand instances are additional instances beyond the group's desired size and are not managed by the reconciler.
    - They are not counted in reconciliation decisions and will not be automatically scaled down if the group size is reduced.

Note whenever a Group or Instance specifies an Instance Type, it must match the templates' arch.

### Dynamic Group Updates via Operator

When the Nstance Operator sends group configuration updates, the Nstance Server processes them as follows:

1. **Receive Update**: Process the group change request from the Operator (CreateGroup/UpdateGroup/DeleteGroup)
2. **Load Dynamic Groups**: Read current `groups/{shard}.jsonc` from object storage (empty map if file doesn't exist)
3. **Validate Changes**: Ensure updates are valid (e.g., cannot override template/subnet pool for static groups)
4. **Write to Object Storage**: Store the updated dynamic groups file atomically
5. **Trigger Reconciliation**: Enqueue reconciliation event to scale group to new desired size
6. **Acknowledge to Operator**: Send success response

## CAPI MachinePool and Machine

Cluster API (CAPI) offers a set of standard CRDs for a `MachinePool` and `Machine`. Nstance defines four infrastructure provider CRDs (`NstanceCluster`, `NstanceMachinePool`, `NstanceMachine`, `NstanceMachineTemplate`) plus its own `NstanceShardGroup` CRD. The Nstance Operator syncs `NstanceMachinePool` and `NstanceMachine` resources to the corresponding Nstance Server `Group` and `Instance`, respectively.

  - Read more about the CRDs in the [Cluster API Integration](cluster-api.md) reference.

Note that a CAPI MachinePool can span multiple zones, but the Nstance architecture is designed such that each Server is responsible for a single zone shard.

Therefore, replica counts flow from the `MachinePool` through the `NstanceMachinePool` controller, which distributes them across `NstanceShardGroup` resources — one per zone shard. The size is divided equally, and when not divisible, larger numbers are assigned to the zone shards in order of how they are listed.

- This provides a level of determinism with how nodes will be scaled in a zone shard not previously achievable with ASGs.

## Group & Instance Attributes

Each `Group` (and `MachinePool`) or `Instance` (and `Machine`) can specify:

- `instance_type`: Instance Type e.g. `t4g.xlarge`
- `vars`: Vars which will be interpolated into userdata (see [Vars](templates-and-vars.md#vars))
- `load_balancers`: Array of load balancer keys for automatic registration (see [Load Balancers](../features/load-balancers.md))

Note that a `MachinePool` has a `replicas` field which flows through the `NstanceMachinePool` controller and is distributed across `NstanceShardGroup` resources as their `size` (desired capacity).

See [Auto-Scaling](../features/auto-scaling.md) for details on group reconciliation, dynamic groups storage, and group deletion.

## Instance Provisioning

The process of provisioning a new VM uses the following workflow:

1. **Generate Instance ID**: Using [puidv7](https://puidv7.dev), with the instance template `kind` as the puidv7 prefix e.g. `knc`.

2. **Generate Registration Nonce JWT**: This embeds the Instance ID and the Nonce gets injected into new VM userdata script, to be handed to the Nstance Agent for registration (see [API Client Registration](#api-client-registration) below).

3. **Prepare User Data**: Download latest userdata template if the instance template specifies a URL, or use the inline userdata template, and substitute variables by processing it as a Go `text/template` i.e. such that the instance ID & Nonce are interpolated into the userdata.

4. **Execute Provider VM Creation**: e.g. AWS RunInstances, to create instance with the interpolated userdata.

## Instance Metadata

Nstance uses a consistent metadata model across all providers to track and manage VM instances. Metadata is categorized into three types:

**1. Identifier Metadata**

The unique key for correlating provider VMs with nstance database records:
- **Instance ID**: Stored as VM name (Proxmox) or `nstance:instance-id` / `nstance-instance-id` tag (cloud providers). This is the globally unique, permanent identifier for an instance — it never changes and is never reused.
- **Provider Instance ID**: The cloud provider's native ID (e.g., AWS instance ID, Proxmox VMID). It should be treated as a reference for provider API calls, not as a stable long-term identifier. The Instance ID is always the authoritative identity. For Proxmox, VMIDs are allocated using a monotonically incrementing counter seeded from the highest existing VMID across the cluster (with a floor of 10000), so in practice they are not reused.

**2. Association Metadata**

Scoping and ownership data used for filtering, garbage collection, and reconciliation. These must be queryable without fetching full VM config:

| Field | Purpose | Storage |
|-------|---------|---------|
| `nstance` ownership | Identifies nstance-managed VMs | Tag |
| `cluster-id` | Cluster ownership | Tag |
| `shard` | Zone shard ownership | Tag |

**3. Annotation Metadata**

Non-authoritative informational data, not used for filtering or reconciliation. Stored with provider for informational purposes (e.g., visible in provider console/UI):

| Field | Purpose |
|-------|---------|
| `group` | Group name for display |
| `kind` | Instance template kind |
| `created-at` | Creation timestamp |

Annotations are stored differently per provider:
- **AWS**: Additional tags with `nstance:` prefix (e.g., `nstance:group`)
- **Google Cloud**: Additional labels with `nstance-` prefix (e.g., `nstance-group`). Google Cloud labels are key-value metadata equivalent to AWS tags. Google Cloud network tags are a separate concept used only for firewall rule targeting and are not used for instance identification.
- **Private cloud providers (Proxmox VE)**: VM description/notes field (not tags)

**Identifier Validation**

All identifiers (cluster ID, shard ID, tenant ID, group ID, subnet pool ID) must be valid across all providers. Rules:
- Pattern: `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
- Characters: lowercase alphanumeric and hyphens only
- No leading/trailing/consecutive hyphens
- Maximum length: 32 characters

## API Client Registration

The Nstance Server generates Registration Nonce JWT using a dedicated private key, as outlined in [Secrets Management](secrets-management.md).

Registration Nonce JWTs are used by any Nstance Agent and the Nstance Operator to register themselves (and their public keys) as API clients. 

* For an Nstance Agent, the Registration Nonce JWT is injected into the userdata of new VM instances. These JWTs are typically extremely short-lived (<5 mins), as it is expected registration will occur typically in about 1 minute, assuming the cloud provider can start the VM within 10-20 seconds.

* For the Nstance Operator, the JWT is expected to be injected into a cluster Secret when the cluster etcd data is seeded. These JWTs are typically slightly longer-lived (default 3 hours), to allow more time for the first Operator container to start running. Operator nonces are generated using the `nstance-admin` CLI:

  ```bash
  nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --secrets-provider object-storage --key-provider env
  nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket my-bucket --secrets-provider object-storage --key-provider env --expiry 1h
  ```

Before a client can register, it must generate a keypair, where the public key will be used for generating a client certificate it can use to create an authenticated gRPC connection for Nstance Server API access.

The registration API requires the client to presents its public key + the Registration Nonce JWT.

Importantly, the Nonce has the identity embedded in it (for agents, the Instance ID, for the Operator, the cluster ID), meaning once the Registration Nonce JWT has been successfully registered it cannot be re-used.

The registration process stores the public key and registration data in object storage, then caches the registration record in the local SQLite database, with empty caches backfilled from object storage. Single-use nonce enforcement differs by client type:

- **Agents**: When the server spawns an instance, it creates an instance record in SQLite with the nonce. When the agent registers, the server verifies the nonce exists AND the instance hasn't already registered (`registered_at` is NULL). This prevents replay attacks while allowing the expected registration flow.

- **Operators**: Since operator nonces are generated externally via CLI (not by the server spawning an instance), the server checks that the nonce does NOT exist in SQLite. On successful registration, a new record is created.

This provides fast nonce validation without requiring object storage queries for every registration attempt.

Registration Nonce JWTs contain a `kind` field of either `agent` or `operator`. For agents, `sub` is the instance ID and must validate to the correct puidv7 prefix for its kind. For operators, `sub` is the cluster ID and must match the server config.

The response from the registration process is a client certificate, which enables the Nstance Agent or Operator to connect to the authenticated gRPC API of the Nstance Server.

The issued certificate also contains the `kind`, which restricts which endpoints the client can access.

## Instance Draining

For Kubernetes nodes, the Server coordinates with the Operator to drain instances before deletion. Drain coordination is only used when the VM is still running (spot termination notices, instance expiry, or unhealthy instances where the provider still reports the VM as running). When an instance is detected as unhealthy via provider status checks (stopping/stopped/deleting/deleted/failed) or not found, drain is skipped because there are no active workloads to migrate — the old instance is deleted immediately after the replacement is created.

**Drain Flow (VM still running):**
1. Server detects instance needs replacement while VM is still running (unhealthy but provider reports running, spot termination notice, or instance expiry)
2. Server creates replacement instance immediately
3. Server marks `drain_started_at` timestamp in SQLite
4. Server notifies Operator via `WatchInstanceEvents` stream: "instance X needs draining, will delete at T+drain_timeout"
5. Operator cordons and drains the Kubernetes node
6. Operator calls `AcknowledgeDrained(instance_id)` when complete
7. Server deletes the old instance

Creating the replacement immediately (step 2) minimizes cluster under-capacity time, since new instances take time to provision, boot, and become ready. The drain and replacement provisioning happen in parallel.

**Drain Timeout:**
- Each group can specify `drain_timeout` in static config (e.g., `"5m"`, `"10m"`)
- If `drain_timeout = 0`: immediate replacement and deletion, no drain coordination
- If `drain_timeout = nil`: uses server default `DrainTimeout` (default 5 minutes, can also be set to `0` to disable)
- Server deletes instance after timeout even if drain not acknowledged

**Idempotency & Leadership Changes:**
- **Drain notifications are idempotent**: Operator ignores duplicate drain requests for same instance
- **Drain acks always trigger deletion**: Server trusts operator and deletes drained instances immediately
- **Leadership changes**: If a leader is elected after drain has started, due to initialisation health re-checks health, it may re-notify the operator before the node has completed draining (operator handles idempotently)
- **SQLite ephemeral**: `drain_started_at` tracked for timeout calculation but lost if a new server is elected (instance re-evaluated as unhealthy if still failing)

The health information about an instance is not persisted to object storage, but is stored in the local SQLite database and considered to be time-based ephemeral data.

If an Nstance Server is recreated, it rebuilds the SQLite cache by loading critical data from object storage and querying cloud provider APIs for current instance state, however for time-based ephemeral data like Health reports it simply waits for the next report to come in. Similarly, drain state is re-evaluated - if the instance is still unhealthy, the new leader will re-initiate the drain process.

Instances in a shutdown state are then cleaned up and removed or archived in object storage.

## Push Updates & Instance Rotation

Nstance Server detects configuration changes and automatically pushes updated files or triggers instance rotations using group-level config hashes.

## Config Hash Design

Two hashes per group, computed from merged config before template rendering:

**1. Runtime Config Hash** (`runtime_config_hash`)
- Covers: `Vars` (map[string]string) + `Files` (map[string]FileConfig)
- Includes anything that can be pushed to existing instances (secrets, env files, json files)
- Changes to config triggers file regeneration and push, to all instances in the group
- Storage:
  - Groups table in SQLite
  - Agent stores at `/opt/nstance-agent/identity/config.hash`
  - Agent reports in health reports
- Initial delivery: Embedded in registration nonce JWT (`config_hash` claim)

**2. Infra Config Hash** (`infra_config_hash`)
- Covers: `Args`, `InstanceType`, `SubnetPool`, `Userdata`, `Kind`, `Arch`
- Includes immutable fields requiring VM replacement
- Changes to config triggers instance rotation (one at a time per group)
- Storage:
  - Groups table in SQLite
  - Instance object storage record (set at provision, never updated)
  - SQLite instances table cache

**Hash Computation**: Uses Go's deterministic `json.Marshal` (auto-sorts map keys) by copying only relevant fields into dedicated hash structs. This prevents accidental hash changes when new fields are added to `MergedConfig`.

## Runtime Config Drift Detection

During health report processing:

1. Agent sends current `config_hash` from local storage
2. Server compares to current group `runtime_config_hash`
3. If different:
   - Regenerate all files (secrets, env, json, string) via FileProcessor
   - Stream files to agent with updated `config_hash` in FileTransfer proto
   - Agent receives files and updates `/opt/nstance-agent/identity/config.hash`
4. Next health report shows matching hash (drift resolved)

Files pushed include all which would typically be sent for a new instance:
- Secrets (fetched from secrets store)
- Templated env files (e.g., `instance.env` with vars)
- Templated json/string files

## Infra Config Drift Detection

During health report processing:

1. Load instance's `infra_config_hash` from SQLite (sourced from object storage instance record)
2. Compare to current group `infra_config_hash`
3. If different and no instances draining:
   - Trigger reconciliation with reason "config-drift"
   - Reconciler schedules rotation using existing cordon/drain/replace flow
   - Only one instance draining per group at a time
4. Replacement instance provisions with new `infra_config_hash`

## Hash Lifecycle

**Provisioning:**
1. Server computes group hashes from merged config
2. Server embeds runtime hash in registration nonce JWT
3. Agent parses JWT and saves to `identity/config.hash`
4. Server stores infra hash in instance object storage record

**Config Changes:**
1. Config reload triggers hash recomputation for all groups
2. Hashes stored in groups table
3. Next health report from each instance triggers drift detection

**Storage Operation Minimization:**
- Group hashes cached in SQLite (no object storage reads during reconciliation)
- Instance infra hash written once at provision (never updated)
- Runtime drift never writes to object storage (agent stores hash locally)

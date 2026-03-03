---
title: "Operator Internals"
weight: 75
description: "Internal architecture of the Nstance Operator: sync mechanics, reconciliation loops, drain coordination, CRDs, and connection management."
---

# Operator Internals

This document covers the internal architecture and implementation details of the [Nstance Operator](../components/nstance-operator.md).

## gRPC API Usage

The operator uses the [Operator Service](server-api.md#operator-service) gRPC API on each shard, including:

- group management: `ListGroups`, `UpsertGroup`, `DeleteGroup`
- instance management: `CreateInstance`, `DeleteInstance`, `GetInstanceStatus`
- drain coordination: `AcknowledgeDrained`
- persistent watch streams: `WatchGroups`, `WatchInstances`, `WatchErrors`

## Operator Sync

The operator maintains **unidirectional sync from Kubernetes to Nstance Server**:

- **Kubernetes is the source of truth** for replica counts and group configuration
- MachinePool.spec.replicas is distributed across shards via NstanceShardGroups
- NstanceShardGroupReconciler calls UpsertGroup on servers to apply changes
- Server state reflects what Kubernetes has requested

**Design Rationale:**

Groups are treated as runtime state managed by the operator and cluster autoscaler, rather than infrastructure configuration managed by Terraform. This separation allows:

1. **Dynamic scaling** - Cluster autoscaler and operators can adjust replica counts without infrastructure changes
2. **GitOps compatibility** - MachinePool resources in Kubernetes can be managed declaratively

However, server config can still define **static groups** to guarantee a minimum number of instances. This solves the bootstrap problem: ensuring enough nodes exist to run the operator itself. Static groups have **restricted editing** - their template, subnets, and shards cannot be modified, preventing accidental breakage of critical infrastructure (i.e. Kubernetes control plane nodes).

### Static Group Protection (Restricted Editing)

Adding a group to server config **enables restricted editing** for that group. Removing it **disables restricted editing** (enabling unrestricted editing). This is enforced at multiple levels:

1. **Server-side validation**: The server rejects `UpsertGroup` requests that attempt to change `template`, `subnets`, or `args` for static groups
2. **Operator-side validation**: A validating admission webhook rejects updates to restricted fields when `status.isStatic` is true

**Static Status Tracking:**
- Each `NstanceShardGroup` has `status.isStatic` set from the server's `GroupStatus.is_static` response
- `NstanceMachinePool.status.isStatic` is aggregated from its NstanceShardGroups - true if ANY shard reports the group as static
- When an admin adds a group to server config, the next `UpsertGroup` response updates `status.isStatic` to true, **enabling restricted editing**
- When an admin removes a group from server config, `status.isStatic` transitions to false, **disabling restricted editing**

**Unrestricted (always allowed):**
- `size` (via MachinePool.spec.replicas)
- `instanceType` (if allowed by template)
- `vars` (for node labels, etc.)

**Restricted (blocked when static):**
- `template` - Defined by server config, cannot be changed
- `subnets` - Defined by server config, cannot be changed
- `shards` - Determined by server config, cannot be changed (prevents deletion of shard groups)

**Initial Bootstrap (on startup after leader election):**
1. Call `ListGroups()` on each shard
2. **All shards must sync successfully before proceeding** - operator will not write to Kubernetes resources until it has data from every expected shard
3. Aggregate groups with same key across shards (sum sizes for initial MachinePool replicas, collect list of shards)
4. For each discovered group, create corresponding CAPI MachinePool + NstanceMachinePool **if missing**
   - `spec.shards` is populated from the shards where the group was discovered
   - `spec.replicas` is set to the sum of sizes across all shards
5. Once created, MachinePool and NstanceMachinePool become the source of truth - server state is not used to update existing resources

**Continuous Sync (K8s → Server):**

1. User or cluster autoscaler modifies MachinePool.spec.replicas
2. NstanceMachinePoolReconciler watches MachinePool and distributes replicas across NstanceShardGroups
3. NstanceShardGroupReconciler watches NstanceShardGroup spec changes
4. When spec.size changes, controller calls `UpsertGroup` on the appropriate shard
5. Server reconciler creates/deletes instances to match requested size

**Watch Streams (Server → Operator):**

The operator opens watch streams for real-time event handling:

1. `WatchGroups()` - Detects new groups for MachinePool creation
2. `WatchInstances()` - Receives drain coordination events
3. `WatchErrors()` - Receives provider errors for Kubernetes events

These streams do NOT modify MachinePool.spec.replicas - Kubernetes remains the source of truth after the initial import/creation of the MachinePool resources.

**Periodic Polling (safety net, every ~30 seconds):**
- Call `ListGroups()` on each shard
- Detect new groups that need MachinePool/NstanceMachinePool creation

**All-Shards Safety Check:**
- The operator tracks all expected shards from the initial connection set
- MachinePool creation only occurs when ALL shards have been successfully synced
- This prevents creating pools with incorrect initial replica counts

**Example**: User scales group "workers" via kubectl:
1. User runs `kubectl scale machinepool workers --replicas=10`
2. NstanceMachinePoolReconciler sees replicas change, updates NstanceShardGroups and distributes size across shards specified in `spec.shards`
3. NstanceShardGroupReconciler calls `UpsertGroup(workers, size=X)` on each shard in `spec.shards`
4. Server reconciler creates new instances to reach desired size

**Example**: Admin adds static group "workers" to server config:
1. Admin updates `config/us-west-2a.jsonc` in object storage (via Terraform)
2. Server boots/reloads config, "workers" group now exists with size=5
3. Operator's periodic sync discovers "workers" via `ListGroups()`
4. Operator creates CAPI MachinePool (replicas=5) + NstanceMachinePool (shards=["us-west-2a"])
5. From this point, MachinePool and NstanceMachinePool are the source of truth - changes flow K8s → Server

**Multi-Shard Aggregation (bootstrap only):**
- Groups with same key across multiple shards are summed for initial MachinePool replicas
- The `spec.shards` field is populated with all shards where the group was discovered
- Example: Group "main" exists in us-west-2a (size=3) and us-west-2b (size=2) → MachinePool created with replicas=5, NstanceMachinePool with shards=["us-west-2a", "us-west-2b"]
- After creation, NstanceMachinePoolReconciler distributes MachinePool.replicas back to the shards specified in `spec.shards`

## Reconciliation Loops

**NstanceMachinePool Controller:**
1. Watch for MachinePool changes (replicas field)
2. Watch for NstanceMachinePool changes (group, template, subnets, instanceType, vars)
3. Ensure NstanceShardGroup exists for each shard
4. Distribute MachinePool.spec.replicas across shards using deterministic algorithm
5. Set NstanceShardGroup spec fields (size, template, instanceType, subnets, vars)
6. Update NstanceMachinePool status with ready state (based on NstanceShardGroup readiness)

**NstanceShardGroup Controller:**
1. Watch for NstanceShardGroup changes
2. When spec changes, call `UpsertGroup` on the shard with full config (size, template, instanceType, subnets, vars)
3. Update conditions[Ready] based on UpsertGroup response
4. Emit Kubernetes events for errors (ProviderError, ShardUnreachable)
5. Handle deletion via finalizer: when resource is deleted, call `DeleteGroup` on the shard to clean up server-side state before allowing Kubernetes resource removal

**Sync Manager:**
1. Watch WatchGroups streams from all shards
2. Maintain cached group state for MachinePool reconciliation
3. Set conditions[ShardReachable] to false when shard connection lost
4. Emit Kubernetes events for state changes

**NstanceMachine Controller:**
1. Watch for Machine creation/deletion
2. Watch for NstanceMachine changes
3. Call `CreateInstance` or `DeleteInstance` as needed
4. Update Machine status with instance state

**On-Demand Pod Watcher:**
1. Watch for Pods with `on-demand.nstance.dev/group` annotation
2. Create CAPI Machine + NstanceMachine resources
3. Server creates instance, agent registers, node joins
4. Pod scheduler places Pod on new node

## Drain Coordination

The Operator watches for instance events from the Server and coordinates Kubernetes node draining. Drain coordination is only used for proactive replacements where the VM is still running (spot termination notices, instance expiry, or unhealthy instances where the provider still reports the VM as running). When instances are detected as unhealthy via provider status checks (stopping/stopped/deleting/deleted/failed) or not found, drain is skipped and the instance is deleted immediately — there are no active workloads to migrate.

**Process:**
1. Operator connects to each shard's `WatchInstances` stream (one per shard)
2. Server streams `InstanceEvent` when instance marked for deletion: `{instance_id, group, delete_at, reason}`
3. Operator maps instance_id to Kubernetes Node (via provider ID matching)
4. Operator cordons and drains the corresponding Kubernetes node
5. When drain completes, Operator calls `AcknowledgeDrained(instance_id)`
6. Server proceeds with instance deletion

**`"deleted"` Events:**

When an nstance-server deletes an instance (due to scale-down, unhealthy replacement, spot termination, expiry, or preemption), it sends a `"deleted"` event via the `WatchInstances` stream. The nstance-operator handles this by cleaning up the corresponding Kubernetes resources:

1. Find the NstanceMachine by `status.instanceID` (using a field index for efficient lookup) if one exists (not the case for NstanceMachinePool/MachinePool-created instances)
2. Update NstanceMachine status: set `ready=false`, add a `ServerDeleted` condition with the deletion reason
3. Find the owning Machine via OwnerReferences
4. Delete the Machine — CAPI's normal ownership cascade handles the rest:
   - Machine deletion triggers NstanceMachine deletion
   - NstanceMachine finalizer sees the `ServerDeleted` condition, skips the `DeleteInstance` call, and removes the finalizer
   - Resources are cleaned up

The Machine is deleted (not the NstanceMachine) because in CAPI the Machine is the lifecycle owner. Deleting the NstanceMachine directly would leave a Machine referencing a missing `infrastructureRef`.

Edge cases are handled gracefully: if the NstanceMachine is not found (already cleaned up), if the Machine is already being deleted, or if the NstanceMachine has no owning Machine (orphaned — deleted directly).

This cleanup path only applies to individually-created Machine/NstanceMachine pairs (e.g. on-demand instances). MachinePool instances don't have individual Machine resources — their lifecycle is managed by the NstanceMachinePool controller.

**Idempotency:**
- Operator MUST handle duplicate drain requests idempotently (same instance may be notified multiple times due to leadership changes)
- If node already cordoned/draining, operator should not re-initiate drain
- Operator should still call `AcknowledgeDrained` even if already drained

**Timeout Handling:**
- Server will delete instance after `drain_timeout` even without acknowledgment
- Operator should complete drain before timeout to avoid abrupt pod termination
- Groups with `drain_timeout = 0` skip drain coordination (immediate deletion)

**Error Handling:**
- If drain fails or hangs, timeout will trigger deletion anyway
- Operator should log drain failures but still acknowledge to unblock deletion
- Server-side timeout prevents indefinite blocking on operator issues

## Node Correlation

The operator sets `Machine.Status.NodeRef` directly, bypassing CAPI's native Node watch mechanism. This links each Machine to the Kubernetes Node it represents.

In the `NstanceMachineReconciler`, after `updateInstanceStatus` receives the `providerID` from the server, the operator:

1. Looks up the Node whose `spec.providerID` matches (using provider ID matching that handles cloud-specific formats like `aws:///zone/i-xxx` and `gce://project/zone/name`)
2. Gets the owning Machine via OwnerReferences
3. Sets `Machine.Status.NodeRef` to reference the Node (if not already set)

This is simpler than CAPI's native mechanism because it avoids kubeconfig secret management, `ControlPlaneInitialized` conditions, and `controlPlaneEndpoint` configuration. The operator already has Node RBAC for drain coordination.

Node correlation only applies to individually-created Machine/NstanceMachine pairs. MachinePool instances don't have individual Machine resources — Node correlation for pool instances is handled by CAPI's MachinePool controller.

## CRDs

CAPI CRDs (used, not owned):

- Cluster
    - `spec.infrastructureRef` - References NstanceCluster
    - Created automatically by the operator on startup. CAPI requires a Cluster resource as the ownership root for MachinePools and Machines — without it, CAPI controllers reject these resources. Nstance manages infrastructure at the pool/machine level, so the Cluster is a formality with no operational role within nstance-operator.
    - <https://cluster-api.sigs.k8s.io/developer/core/controllers/cluster>

- MachinePool
    - `spec.replicas` - Desired instance count (cluster autoscaler modifies this)
    - `spec.template.spec.infrastructureRef` - References NstanceMachinePool
    - <https://cluster-api.sigs.k8s.io/developer/core/controllers/machine-pool>
    - <https://cluster-api.sigs.k8s.io/tasks/experimental-features/machine-pools>

- Machine
    - `spec.infrastructureRef` - References NstanceMachineTemplate
    - `status.nodeRef` - Set by the operator to link Machine to Kubernetes Node (see Node Correlation below)
    - <https://cluster-api.sigs.k8s.io/developer/core/controllers/machine>

Nstance CRDs (minimal set):

- NstanceCluster
    - Minimal stub that satisfies the CAPI infrastructure cluster contract
    - `status.initialization.provisioned` - Set to true immediately (Nstance manages infrastructure at the pool/machine level, not the cluster level)
    - `status.conditions[Ready]` - Always true
    - Created automatically by the operator on startup

- NstanceMachinePool
    - `spec.group` - Name of Nstance Group used in server config/groups file
    - `spec.shards` - **Required**. List of shards this group should be distributed across (e.g., `["us-west-2a", "us-west-2b"]`). Each shard will have a corresponding NstanceShardGroup created. Replicas from the MachinePool are distributed across these shards.
    - `spec.template` - Template name for new dynamic groups (required if group doesn't exist in static config, must not be set for static groups)
    - `spec.subnets` - Optional subnets for new dynamic groups (uses template defaults if not specified, must not be set for static groups)
    - `spec.instanceType` - Optional override (must be allowed by the Group)
    - `spec.vars` - Additional vars merged with template vars (enables node labels, etc.)
    - `status.isStatic` - True if this group is backed by static server config (template/subnets cannot be modified)
    - `status.template` - Actual template being used by the group on the server
    - `status.subnets` - Actual subnets being used by the group on the server
    - Used by cluster autoscaler via MachinePool

- NstanceMachine
    - `spec.groupRef` - Reference to Nstance Group
    - `spec.instanceType` - Optional override
    - `spec.vars` - Additional vars
    - `status.instanceID` - Nstance instance ID (server-generated)
    - `status.providerID` - Cloud provider instance ID
    - `status.ready` - Whether instance is ready
    - Represents actual infrastructure machine instance

- NstanceMachineTemplate
    - `spec.template.spec.groupRef` - Reference to Nstance Group
    - `spec.template.spec.instanceType` - Optional override
    - `spec.template.spec.vars` - Additional vars
    - Immutable template pattern (CAPI standard)
    - Used to stamp out Machine → NstanceMachine pairs

- NstanceShardGroup
    - One resource per (group, shard) pair for per-shard visibility
    - `metadata.name` - Format: `{group}--{shard}` (e.g., `workers--us-west-2a`)
    - `metadata.labels` - `nstance.dev/group` and `nstance.dev/shard`
    - `metadata.ownerReferences` - Owned by NstanceMachinePool
    - `spec.group` - Name of the Nstance Group
    - `spec.shard` - shard identifier
    - `spec.size` - Desired size for THIS shard (from replica distribution)
    - `spec.template` - Template name (copied from NstanceMachinePool)
    - `spec.instanceType` - Instance type override (copied from NstanceMachinePool)
    - `spec.subnets` - Subnets for this group (copied from NstanceMachinePool)
    - `spec.vars` - Vars merged with template vars (copied from NstanceMachinePool)
    - `status.observedGeneration` - Generation last processed by the controller (standard K8s pattern to avoid reconcile loops)
    - `status.isStatic` - True if this group is backed by static server config on this shard
    - `status.config` - Merged configuration from server (template, subnets, instanceType, vars)
    - `status.lastSyncTime` - When status was last synced from server
    - `status.conditions` - Ready, ShardReachable, ConfigValid
    - Created automatically by NstanceMachinePool controller
    - NstanceShardGroup controller calls UpsertGroup on the shard

**Cluster Configuration (not a CRD):**
- ConfigMap: shard endpoints `{"us-east-1a": "[2600:1f18:1234:5678::a]:8993", ...}`
- Secret: registration nonce JWT (bootstrap)
- Secret: operator certificate (created by operator after registration)

Note that the NstanceMachinePool CRD does not have a size field, as we use the `replicas` field from the MachinePool CRD to determine the size of the Nstance Group.

## Connection Management

**Multi-Shard Connections:**
- Operator maintains persistent gRPC connections to all shards
- Server endpoints configured via ConfigMap (e.g., `[2600:1f18:1234:5678::a]:8993`)
- Each connection uses the same mTLS certificate
- Connections use keepalive and automatic reconnection

**Service Discovery:**
- Server endpoints use stable leader network IPs (configured per shard)
- Active shard leader assigns the leader network (ENI attachment on AWS, alias IP on GCP) via s3lect election
- IP address remains stable as leadership changes between server instances
- Health endpoint (`/leader/health`) indicates current leader status
- Operator should retry failed connections with exponential backoff
- ConfigMap can be updated to add/remove shards without operator restart

**Stream Management:**
- Each shard has a `WatchGroups` stream for group sync
- Each shard has a `WatchInstances` stream for drain coordination
- Streams reconnect automatically on disconnect with exponential backoff
- Operator ignores/noops duplicate drain events for an instance
- Server sends current drain state as initial snapshot on `WatchInstances` connect
- Server sends current group state as initial snapshot on `WatchGroups` connect

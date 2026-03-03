---
title: "Auto-Scaling"
weight: 10
description: "How Nstance automatically reconciles instance groups to maintain desired capacity."
---

# Auto-Scaling

Nstance Server automatically reconciles instance groups to maintain desired capacity through an event-driven reconciliation system.

## Group Reconciliation

The Nstance Server (when elected as shard leader) continuously reconciles groups to ensure actual instance counts match desired group sizes through an event-driven reconciliation system:

**Reconciliation Triggers:**
- **Initial Reconciliation**: On server startup or when becoming shard leader, all groups are reconciled to desired state
- **Group Configuration Changes**: When group size, instance type, or vars are updated (via Operator API or config changes)
- **Health-Based Replacement**: When instances become unhealthy (gRPC disconnect, missed health reports, provider status checks)
- **Instance Expiry**: When instances exceed configured server-wide age limits (eligibleAge or forcedAge)
- **Instance Deletion**: When instances are deleted, groups are backfilled to maintain desired size

**Reconciliation Logic:**
- **Instance Counting**: Only counts managed instances created by the reconciler; on-demand instances created via the Operator API are excluded from reconciliation decisions
- **Scale Up**: If actual < desired, create new instances (rate-limited, with subnet capacity checking)
- **Scale Down**: If actual > desired, delete oldest managed instances (waits for unhealthy instances to be replaced first)
- **Unhealthy Replacement**: Unhealthy managed instances are automatically replaced to maintain group health and size
- **Instance Expiry**: Instances exceeding age limits are expired with replacement, following drain coordination (see [Instance Expiry](instance-expiry.md))

**Priority Order:**

Reconciliation operations are prioritized as follows:
1. Scale Down (reduce group size)
2. Forced Expiry (compliance requirements)
3. Unhealthy Replacement (maintain health)
4. Opportunistic Expiry (routine rotation)
5. Scale Up (increase group size)

## Dynamic Groups Storage

- Static groups are defined in `config/{shard}.jsonc` (enabling restricted editing for those groups)
- Dynamic groups (created via Operator API) are stored in `groups/{shard}.jsonc` (and have unrestricted editing)
- Dynamic groups override static groups by key, but only unrestricted fields (e.g. `size`, `instance_type`, `vars`) can be changed
- Restricted fields (e.g. `template`, `subnet_pool`) from static groups cannot be overridden, preventing breaking changes to critical groups

## Group Deletion

When a group is deleted (removed from config or via Operator API):
- The reconciler gracefully scales down all managed instances to 0
- Drain coordination is followed for each instance (if `drain_timeout > 0`)
- This ensures clean shutdown rather than immediate termination

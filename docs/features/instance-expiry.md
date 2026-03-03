---
title: "Instance Expiry"
weight: 50
description: "Automatic instance rotation based on configurable age limits for compliance and security."
---

# Instance Expiry

Nstance Server supports automatic instance expiry based on configurable age limits, enabling predictable instance rotation for compliance, security, or operational reasons. Expiry follows the same drain coordination workflow as unhealthy instance replacement.

## Configuration

The server can be configured with optional age limits in the `server.expiry` block:

- `eligibleAge`: Duration string (e.g., `"21d"`, `"168h"`). Managed instances older than this become eligible for opportunistic expiry.
- `forcedAge`: Duration string (e.g., `"30d"`, `"720h"`). Managed instances older than this are expired immediately regardless of cluster state.
- `ondemandAge`: Duration string (e.g., `"7d"`, `"168h"`). On-demand instances older than this are expired immediately with drain coordination.

If none are configured, instances never expire automatically. The `eligibleAge` and `forcedAge` settings apply to managed instances (created by group reconciliation), while `ondemandAge` applies to on-demand instances (created via Operator API).

## Expiry Logic

1. **Timer-Based Scheduling**: Each group maintains an expiry timer that fires when the oldest instance reaches `eligibleAge` (or `forcedAge` if sooner). This ensures expiry checks happen at the exact moment an instance becomes eligible, with no polling overhead. After each expiry check, the timer is rescheduled for the next oldest instance.

2. **Opportunistic Expiry** (server.expiry.eligibleAge):
   - Identifies instances where `current_age > eligibleAge`
   - Only proceeds if no instances in the group are currently draining or unhealthy
   - Selects the oldest eligible instance to minimize disruption
   - Creates replacement instance immediately to maintain group size

3. **Forced Expiry** (server.expiry.forcedAge):
   - Identifies instances where `current_age > forcedAge`
   - Expires immediately regardless of draining/unhealthy state
   - Bypasses the "one-at-a-time" constraint for compliance scenarios

4. **Per-Group Independence**: Each group schedules its own expiry timer independently, ensuring a long-draining instance in one group doesn't block expiry checks in other groups.

5. **Replacement Before Drain**: When an instance is selected for expiry (or unhealthy replacement):
   - A replacement instance is created first, ensuring cluster capacity during drain
   - The group temporarily has `desired + 1` instances (capped at 1 extra for safety)
   - The old instance is then marked for drain
   - After drain completion or timeout, the old instance is deleted

6. **Drain Coordination**: Once an instance is marked for drain:
   - If `drainTimeout > 0`, coordinate drain with Operator via `WatchInstanceEvents`
   - Wait for drain acknowledgment or timeout
   - Delete old instance after drain completion

7. **One-at-a-Time Constraint**: Only one expiry operation per group at any time to prevent cluster overload. The server tracks draining state across all instances in the group.

8. **On-Demand Instance Expiry**: On-demand instances (created via Operator API) are checked separately during reconciliation. When an on-demand instance exceeds `ondemandAge`, it is immediately marked for expiry with drain coordination, regardless of other draining operations. This ensures temporary on-demand instances don't persist indefinitely.

## Priority Order

Reconciliation operations are prioritized as follows:
1. Scale Down (reduce group size)
2. Forced Expiry (compliance requirements)
3. Unhealthy Replacement (maintain health)
4. Opportunistic Expiry (routine rotation)
5. Scale Up (increase group size)

## Age Calculation

Instance age is calculated from the creation timestamp stored in the local SQLite database. This ensures accurate tracking even across server restarts or leadership changes.

## Example Scenarios

**Opportunistic Expiry**:
- Instance created 22 days ago
- `server.expiry.eligibleAge: "21d"` configured
- No other draining instances → expire opportunistically
- Replacement created, old instance drained and deleted

**Forced Expiry**:
- Instance created 31 days ago
- `server.expiry.forcedAge: "30d"` configured
- Other instances draining → force expiry regardless
- Immediate replacement and drain initiation

**Blocked Opportunistic**:
- Instance created 22 days ago
- `server.expiry.eligibleAge: "21d"` configured
- Another instance currently draining → expiry deferred, timer rescheduled for next oldest instance

**On-Demand Expiry**:
- On-demand instance created 8 days ago
- `server.expiry.ondemandAge: "7d"` configured
- Immediately marked for expiry with drain coordination
- Replacement not created (on-demand instances are temporary)

This design ensures predictable instance lifecycle management while maintaining cluster stability and following existing operational patterns.

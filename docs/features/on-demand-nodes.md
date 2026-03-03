---
title: "On-Demand Nodes"
weight: 70
description: "Provisioning individual instances on-demand via Pod annotations for specific workload requirements."
---

# On-Demand Nodes

On-demand instances are individual, on-time instances created via the Operator API on request. This is separate from standard auto-scaling instances created by the Nstance Server reconciler.

All instances references a Group to derive its configuration. On-demand instances are additional instances beyond the group's desired size and are not managed by the Nstance Server reconciler — they are not counted in reconciliation decisions and will not be automatically scaled down if the group size is reduced.

## Pod-Triggered Provisioning

The Nstance Operator watches for Pods with the `on-demand.nstance.dev/group` annotation.

If found, it will create a new CAPI Machine resource, and looks for the following annotations to use for the NstanceMachine fields:

- `groupRef:` = `on-demand.nstance.dev/group`: the name of the Nstance Group to use for the on-demand node
- `instanceType:` = `on-demand.nstance.dev/instance-type`: the instance type to use for the on-demand node

## Lifecycle

1. Pod created with `on-demand.nstance.dev/group` annotation
2. Operator creates CAPI Machine + NstanceMachine resources
3. Server creates instance, agent registers, node joins
4. Pod scheduler places Pod on new node

On-demand instances can be configured with a maximum age via `server.expiry.ondemandAge` to ensure temporary instances don't persist indefinitely. See [Instance Expiry](instance-expiry.md) for details.
